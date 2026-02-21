package torrent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// QueueManager manages the torrent generation queue
type QueueManager struct {
	db         *sql.DB
	generator  *Generator
	client     *Client
	serverID   string
	maxWorkers int

	// Orchestration: main server config for hash-check API
	mainServerURL  string
	remoteServerID string
	macAddress     string

	mu              sync.Mutex
	workers         int
	stopChan        chan struct{}
	lastMaintenance time.Time // Last time expensive maintenance queries ran
	lastStatusLog   time.Time // Last time queue status was logged
	lastTorrentSync time.Time // Last time we checked main server for missing torrents
}

// QueueItem represents an item in the generation queue
type QueueItem struct {
	ID          string
	PackageID   string
	PackagePath string
	PackageName string
	Status      string
	QueuedAt    time.Time
}

// NewQueueManager creates a new queue manager
func NewQueueManager(db *sql.DB, generator *Generator, client *Client, serverID string, maxWorkers int) *QueueManager {
	if maxWorkers <= 0 {
		maxWorkers = 2 // Default: 2 concurrent generations
	}

	return &QueueManager{
		db:         db,
		generator:  generator,
		client:     client,
		serverID:   serverID,
		maxWorkers: maxWorkers,
		stopChan:   make(chan struct{}),
	}
}

// SetMainServerConfig configures the QueueManager to check with the main server
// before hashing. Only call this on client servers (not the main server itself).
func (qm *QueueManager) SetMainServerConfig(mainServerURL, remoteServerID, macAddress string) {
	qm.mainServerURL = mainServerURL
	qm.remoteServerID = remoteServerID
	qm.macAddress = macAddress
	log.Printf("[queue] Hash orchestration enabled - will check main server before hashing")
}

// ShouldHash asks the main server (orchestrator) whether this server should hash
// the package identified by assetMapUUID. If no main server is configured (i.e. we ARE
// the main server), always returns true. Fails open: if the API call fails, returns true
// so hashing is never blocked by network issues.
func (qm *QueueManager) ShouldHash(assetMapUUID string) bool {
	if qm.mainServerURL == "" {
		return true // Main server mode: always hash locally
	}

	url := fmt.Sprintf("%s/api/v1/servers/%s/hash-check", qm.mainServerURL, qm.remoteServerID)

	body, err := json.Marshal(map[string]string{"assetmap_uuid": assetMapUUID})
	if err != nil {
		log.Printf("[orchestration] Failed to marshal hash-check request: %v", err)
		return true // Fail open
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		log.Printf("[orchestration] Failed to create hash-check request: %v", err)
		return true
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", qm.remoteServerID)
	req.Header.Set("X-MAC-Address", qm.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[orchestration] Hash-check API call failed: %v", err)
		return true // Fail open: if main server unreachable, hash locally
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("[orchestration] Hash-check returned status %d", resp.StatusCode)
		return true // Fail open
	}

	var result struct {
		Action        string  `json:"action"`
		HashingServer string  `json:"hashing_server,omitempty"`
		Progress      float64 `json:"progress,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[orchestration] Failed to decode hash-check response: %v", err)
		return true
	}

	switch result.Action {
	case "hash":
		return true
	case "wait":
		log.Printf("[orchestration] Skipping hash for %s - server '%s' is already hashing (%.1f%%)", assetMapUUID, result.HashingServer, result.Progress)
		return false
	case "download":
		log.Printf("[orchestration] Skipping hash for %s - torrent already exists, will download", assetMapUUID)
		return false
	default:
		log.Printf("[orchestration] Unknown action '%s', proceeding with hash", result.Action)
		return true
	}
}

// StartSeedingExisting implements the scanner.TorrentQueue interface for cross-site co-seeding.
// It delegates to the torrent client's split-path seeding capability so that:
//   - Large MXF files are read from mxfPath (the original RosettaBridge library location)
//   - Small XML metadata files are read from xmlShadowPath (canonical copies from main server)
func (qm *QueueManager) StartSeedingExisting(torrentBytes []byte, mxfPath, xmlShadowPath, packageID, torrentID string) error {
	if qm.client == nil {
		return fmt.Errorf("no torrent client available for split-path seeding")
	}
	return qm.client.StartSeedingWithSplitPath(torrentBytes, mxfPath, xmlShadowPath, packageID, torrentID)
}

// Start starts the queue processor. Only one process per (database, server_id) should run this;
// on the main server there is a single process; on client sites each runs its own process with its own server_id.
func (qm *QueueManager) Start(ctx context.Context) {
	log.Printf("Starting torrent queue manager (server_id=%s pid=%d max_workers=%d)...", qm.serverID, os.Getpid(), qm.maxWorkers)

	// Reset any stale "generating" items back to "queued" on startup
	// This handles items that were marked as generating but system crashed/restarted
	qm.resetStaleGeneratingItems()

	// Upload any locally-generated torrents that haven't been sent to the main server yet
	if qm.mainServerURL != "" {
		go qm.uploadPendingTorrents()
	}

	// Use a 5-second tick to keep workers busy; maintenance tasks run on their own schedule
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			qm.stopChan <- struct{}{}
			return
		case <-ticker.C:
			qm.processQueue(ctx)
		}
	}
}

// resetStaleGeneratingItems resets any items stuck in "generating" status back to "queued"
func (qm *QueueManager) resetStaleGeneratingItems() {
	query := `
		UPDATE torrent_queue
		SET status = 'queued', 
		    progress_percent = 0, 
		    current_file = NULL,
		    started_at = NULL
		WHERE server_id = $1 AND status = 'generating'
	`

	result, err := qm.db.Exec(query, qm.serverID)
	if err != nil {
		log.Printf("Error resetting stale generating items: %v", err)
		return
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		log.Printf("Reset %d stale 'generating' items back to 'queued' status", affected)
	}
}

// markUnpickableQueuedItemsAsFailed marks queued items that have no server_dcp_inventory
// entry (so getNextQueuedItem would never return them) as failed with a specific error
// per package so the user sees the real reason (which package, and why it can't be hashed).
// Returns the number of items marked failed.
func (qm *QueueManager) markUnpickableQueuedItemsAsFailed() int64 {
	selQuery := `
		SELECT tq.id, tq.package_id, dp.package_name
		FROM torrent_queue tq
		JOIN dcp_packages dp ON tq.package_id = dp.id
		WHERE tq.server_id = $1
		  AND tq.status = 'queued'
		  AND NOT EXISTS (
		    SELECT 1 FROM server_dcp_inventory inv
		    WHERE inv.package_id = tq.package_id AND inv.server_id = tq.server_id
		  )
	`
	rows, err := qm.db.Query(selQuery, qm.serverID)
	if err != nil {
		log.Printf("Error listing unpickable queue items: %v", err)
		return 0
	}
	defer rows.Close()

	var queueID, packageID, packageName string
	var count int64
	for rows.Next() {
		if err := rows.Scan(&queueID, &packageID, &packageName); err != nil {
			log.Printf("Error scanning unpickable row: %v", err)
			continue
		}
		// Check if this package exists on any other server (helps explain the failure)
		var otherServers int
		_ = qm.db.QueryRow(
			`SELECT COUNT(DISTINCT server_id) FROM server_dcp_inventory WHERE package_id = $1 AND server_id != $2`,
			packageID, qm.serverID,
		).Scan(&otherServers)

		var errMsg string
		if otherServers > 0 {
			errMsg = fmt.Sprintf("%s: DCP is on %d other server(s) but not on this server. Add this DCP to this server's scan path and rescan, then retry.", packageName, otherServers)
		} else {
			errMsg = fmt.Sprintf("%s: Not on this server's inventory (no local path). The DCP is not on this server's scan path, or was moved/removed after being queued. Rescan this server or add the path, then retry.", packageName)
		}

		_, err := qm.db.Exec(
			`UPDATE torrent_queue SET status = 'failed', error_message = $1 WHERE id = $2`,
			errMsg, queueID,
		)
		if err != nil {
			log.Printf("Error marking queue item %s as failed: %v", queueID, err)
			continue
		}
		count++
		log.Printf("Queue: marked %s as failed (no server_dcp_inventory on this server) [pid=%d]", packageName, os.Getpid())
	}
	return count
}

// markStuckGeneratingItemsAsFailed marks items that have been in "generating" status
// for more than 3 hours as failed. For local items (this server), it checks started_at.
// It also checks ALL servers' items that haven't had a synced_at update in 3+ hours
// (i.e. client stopped reporting progress), so the main server can clean up stale
// client queue items too.
func (qm *QueueManager) markStuckGeneratingItemsAsFailed() int64 {
	// Check this server's own items (started 3+ hours ago)
	localQuery := `
		UPDATE torrent_queue
		SET status = 'failed',
		    error_message = 'Hashing timed out: no progress for over 3 hours. The hash job appeared stuck at ' || ROUND(progress_percent::numeric, 1) || '%. Retry to attempt again.',
		    completed_at = NOW()
		WHERE server_id = $1
		  AND status = 'generating'
		  AND started_at IS NOT NULL
		  AND started_at < NOW() - INTERVAL '3 hours'
	`

	result, err := qm.db.Exec(localQuery, qm.serverID)
	if err != nil {
		log.Printf("Error marking stuck local generating items as failed: %v", err)
		return 0
	}
	localAffected, _ := result.RowsAffected()

	// Also check remote servers' items that haven't been synced in 3+ hours
	// (client stopped reporting progress — likely offline or crashed)
	globalQuery := `
		UPDATE torrent_queue
		SET status = 'failed',
		    error_message = 'Hashing timed out: no progress update received for over 3 hours. The hash job appeared stuck at ' || ROUND(progress_percent::numeric, 1) || '%. The client may have gone offline. Retry to attempt again.',
		    completed_at = NOW()
		WHERE server_id != $1
		  AND status = 'generating'
		  AND synced_at IS NOT NULL
		  AND synced_at < NOW() - INTERVAL '3 hours'
	`
	result2, err := qm.db.Exec(globalQuery, qm.serverID)
	if err != nil {
		log.Printf("Error marking stuck remote generating items as failed: %v", err)
	}
	remoteAffected, _ := result2.RowsAffected()

	total := localAffected + remoteAffected
	if total > 0 {
		log.Printf("Marked %d stuck 'generating' items as failed (%d local, %d remote, 3+ hours with no progress)", total, localAffected, remoteAffected)
	}
	return total
}

// processQueue checks for queued items and processes them.
// Fills ALL available worker slots in one call rather than just one.
func (qm *QueueManager) processQueue(ctx context.Context) {
	now := time.Now()

	// Run expensive maintenance tasks every 60 seconds (not every tick)
	if now.Sub(qm.lastMaintenance) >= 60*time.Second {
		qm.lastMaintenance = now
		qm.markUnpickableQueuedItemsAsFailed()
		qm.markStuckGeneratingItemsAsFailed()
	}

	// Check for missing torrents on main server every 5 minutes and upload any we have locally
	if qm.mainServerURL != "" && now.Sub(qm.lastTorrentSync) >= 5*time.Minute {
		qm.lastTorrentSync = now
		go qm.syncMissingTorrents()
	}

	// Log queue status every 60 seconds for visibility
	if now.Sub(qm.lastStatusLog) >= 60*time.Second {
		qm.lastStatusLog = now
		qm.logQueueStatus()
	}

	// Fill all available worker slots
	for {
		qm.mu.Lock()
		currentWorkers := qm.workers
		qm.mu.Unlock()

		if currentWorkers >= qm.maxWorkers {
			return
		}

		// Get next queued item
		item, err := qm.getNextQueuedItem()
		if err != nil {
			if err != sql.ErrNoRows {
				log.Printf("Error getting queued item: %v", err)
			}
			return // No more items or error
		}

		log.Printf("[queue] Starting generation: %s (worker %d/%d)", item.PackageName, currentWorkers+1, qm.maxWorkers)

		// Increment worker count
		qm.mu.Lock()
		qm.workers++
		qm.mu.Unlock()

		// Process in goroutine
		go func(it *QueueItem) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("PANIC in torrent queue worker for %s: %v", it.PackageName, r)
					qm.updateQueueStatus(it.ID, "failed", 0, fmt.Sprintf("Internal error (panic): %v", r))
				}
				qm.mu.Lock()
				qm.workers--
				qm.mu.Unlock()
			}()

			qm.processItem(ctx, it)
		}(item)
	}
}

// logQueueStatus logs a summary of the current queue state
func (qm *QueueManager) logQueueStatus() {
	status, err := qm.GetQueueStatus()
	if err != nil {
		return
	}

	total := 0
	for _, v := range status {
		total += v
	}

	if total == 0 {
		return // Nothing in queue, don't log
	}

	qm.mu.Lock()
	activeWorkers := qm.workers
	qm.mu.Unlock()

	log.Printf("[queue] Status: queued=%d, generating=%d, completed=%d, failed=%d (workers: %d/%d)",
		status["queued"], status["generating"], status["completed"], status["failed"],
		activeWorkers, qm.maxWorkers)
}

// getNextQueuedItem atomically claims the next queued item from database by
// updating its status to 'generating' and returning it. This prevents the same
// item from being picked twice when filling multiple worker slots.
// Requires a matching server_dcp_inventory row so we have local_path for hashing.
func (qm *QueueManager) getNextQueuedItem() (*QueueItem, error) {
	// Two-step atomic claim: find then update in a transaction
	tx, err := qm.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Find the next queued item with inventory (FOR UPDATE locks the row)
	// Order by total_size_bytes ASC so smaller DCPs get hashed first (faster turnaround)
	selectQuery := `
		SELECT tq.id, tq.package_id, inv.local_path, dp.package_name, tq.queued_at
		FROM torrent_queue tq
		JOIN dcp_packages dp ON tq.package_id = dp.id
		JOIN server_dcp_inventory inv ON inv.package_id = tq.package_id AND inv.server_id = tq.server_id
		WHERE tq.server_id = $1 AND tq.status = 'queued'
		ORDER BY COALESCE(dp.total_size_bytes, 0) ASC, tq.queued_at ASC
		LIMIT 1
		FOR UPDATE OF tq SKIP LOCKED
	`

	item := &QueueItem{}
	err = tx.QueryRow(selectQuery, qm.serverID).Scan(
		&item.ID, &item.PackageID, &item.PackagePath, &item.PackageName, &item.QueuedAt,
	)
	if err != nil {
		return nil, err
	}

	// Claim it by marking as 'generating'
	_, err = tx.Exec(
		`UPDATE torrent_queue SET status = 'generating', started_at = NOW() WHERE id = $1`,
		item.ID,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	item.Status = "generating"
	return item, nil
}

// processItem generates torrent for a queue item
func (qm *QueueManager) processItem(ctx context.Context, item *QueueItem) {
	log.Printf("Processing torrent generation for package: %s (%s)", item.PackageName, item.PackageID)

	// Generate torrent
	mi, infoHash, err := qm.generator.GenerateTorrent(ctx, item.PackagePath, item.PackageID, qm.serverID)
	if err != nil {
		log.Printf("Failed to generate torrent for %s: %v", item.PackageName, err)
		qm.updateQueueStatus(item.ID, "failed", 0, fmt.Sprintf("Generation failed: %v", err))
		return
	}

	log.Printf("Successfully generated torrent for %s (info_hash: %s)", item.PackageName, infoHash)

	// Save torrent to database
	err = qm.generator.SaveTorrentToDatabase(mi, infoHash, item.PackageID, qm.serverID)
	if err != nil {
		log.Printf("Failed to save torrent to database for %s: %v", item.PackageName, err)
		qm.updateQueueStatus(item.ID, "failed", 100, fmt.Sprintf("Failed to save: %v", err))
		return
	}

	// Get torrent ID from database
	torrentID, err := qm.getTorrentID(infoHash)
	if err != nil {
		log.Printf("Failed to get torrent ID for %s: %v", item.PackageName, err)
		return
	}

	// Read the exact torrent bytes from the database.
	// We must NOT re-encode mi here because bencode.NewEncoder().Encode(mi)
	// can produce different bytes than bencode.Marshal(mi) used in SaveTorrentToDatabase,
	// which would change the info_hash and cause all piece verification to fail.
	var torrentBytes []byte
	err = qm.db.QueryRow("SELECT torrent_file FROM dcp_torrents WHERE info_hash = $1", infoHash).Scan(&torrentBytes)
	if err != nil {
		log.Printf("Failed to read torrent file from database for %s: %v", item.PackageName, err)
		return
	}

	// Start seeding
	err = qm.client.StartSeeding(torrentBytes, item.PackagePath, item.PackageID, torrentID)
	if err != nil {
		log.Printf("Failed to start seeding for %s: %v", item.PackageName, err)
		// Don't fail the queue item - torrent was created successfully
	} else {
		log.Printf("Started seeding %s", item.PackageName)
	}

	// Upload torrent to main server so it's available for distribution
	if qm.mainServerURL != "" {
		log.Printf("[torrent-upload] Uploading torrent for %s (info_hash=%s) to main server...", item.PackageName, infoHash)
		if err := qm.uploadTorrentToMainServer(item.PackageID, infoHash, torrentBytes); err != nil {
			log.Printf("[torrent-upload] WARNING: Failed to upload torrent for %s to main server: %v", item.PackageName, err)
			log.Printf("[torrent-upload] The torrent was saved locally and will be retried on next sync cycle")
			// Don't fail the queue item - the torrent was generated and saved locally
		} else {
			log.Printf("[torrent-upload] Successfully uploaded torrent for %s to main server", item.PackageName)
		}
	} else {
		log.Printf("[torrent-upload] Skipping upload (this IS the main server) for %s", item.PackageName)
	}

	log.Printf("Completed torrent generation and seeding for %s", item.PackageName)
}

// RegisterTorrentRequest is the payload sent to the main server to register a torrent
type RegisterTorrentRequest struct {
	AssetMapUUID   string `json:"assetmap_uuid"`
	InfoHash       string `json:"info_hash"`
	TorrentFile    []byte `json:"torrent_file"` // base64-encoded by json.Marshal
	PieceSize      int    `json:"piece_size"`
	TotalPieces    int    `json:"total_pieces"`
	FileCount      int    `json:"file_count"`
	TotalSizeBytes int64  `json:"total_size_bytes"`
	ServerID       string `json:"server_id"`
}

// uploadTorrentToMainServer uploads the generated torrent file and its metadata to the
// main server so it knows the DCP is ready for distribution. The main server resolves
// the package by assetmap_uuid (since package IDs differ between client and server).
func (qm *QueueManager) uploadTorrentToMainServer(packageID, infoHash string, torrentBytes []byte) error {
	// Look up assetmap_uuid from local database
	var assetMapUUID string
	err := qm.db.QueryRow("SELECT assetmap_uuid FROM dcp_packages WHERE id = $1", packageID).Scan(&assetMapUUID)
	if err != nil {
		return fmt.Errorf("failed to get assetmap_uuid for package %s: %w", packageID, err)
	}

	// Get torrent metadata from local database
	var pieceSize, totalPieces, fileCount int
	var totalSizeBytes int64
	err = qm.db.QueryRow(
		"SELECT piece_size, total_pieces, file_count, total_size_bytes FROM dcp_torrents WHERE info_hash = $1",
		infoHash,
	).Scan(&pieceSize, &totalPieces, &fileCount, &totalSizeBytes)
	if err != nil {
		return fmt.Errorf("failed to get torrent metadata for %s: %w", infoHash, err)
	}

	// Build the request payload
	payload := RegisterTorrentRequest{
		AssetMapUUID:   assetMapUUID,
		InfoHash:       infoHash,
		TorrentFile:    torrentBytes,
		PieceSize:      pieceSize,
		TotalPieces:    totalPieces,
		FileCount:      fileCount,
		TotalSizeBytes: totalSizeBytes,
		ServerID:       qm.remoteServerID,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal torrent upload request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/torrents", qm.mainServerURL)
	log.Printf("[torrent-upload] POST %s (assetmap_uuid=%s, info_hash=%s, size=%d bytes, torrent_file=%d bytes)",
		url, assetMapUUID, infoHash, totalSizeBytes, len(torrentBytes))

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", qm.remoteServerID)
	req.Header.Set("X-MAC-Address", qm.macAddress)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("main server returned status %d: %s", resp.StatusCode, string(respBody))
	}

	log.Printf("[torrent-upload] Main server accepted torrent (info_hash=%s): %s", infoHash, string(respBody))
	return nil
}

// getTorrentID retrieves torrent ID by info hash
func (qm *QueueManager) getTorrentID(infoHash string) (string, error) {
	var id string
	query := `SELECT id FROM dcp_torrents WHERE info_hash = $1`
	err := qm.db.QueryRow(query, infoHash).Scan(&id)
	return id, err
}

// updateQueueStatus updates the status of a queue item
func (qm *QueueManager) updateQueueStatus(queueID, status string, progress float64, errorMsg string) error {
	var errorField sql.NullString
	if errorMsg != "" {
		errorField.String = errorMsg
		errorField.Valid = true
	}

	query := `
		UPDATE torrent_queue
		SET status = $1, progress_percent = $2, error_message = $3
		WHERE id = $4
	`

	_, err := qm.db.Exec(query, status, progress, errorField, queueID)
	return err
}

// AddToQueue adds a package to the torrent generation queue.
// If a stale entry exists (failed/cancelled/completed), it resets it to 'queued'
// so the package gets re-processed. This handles restart resilience.
func (qm *QueueManager) AddToQueue(packageID string) error {
	// Check if already actively in queue (queued or generating)
	var exists bool
	checkQuery := `
		SELECT EXISTS(SELECT 1 FROM torrent_queue WHERE package_id = $1 AND server_id = $2 AND status IN ('queued', 'generating'))
	`
	err := qm.db.QueryRow(checkQuery, packageID, qm.serverID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check queue: %w", err)
	}

	if exists {
		return nil // Already actively queued
	}

	// Insert new entry, or reset stale entry (failed/cancelled/completed) to 'queued'.
	// The WHERE clause ensures we never overwrite an active 'queued'/'generating' entry.
	id := uuid.New().String()
	now := time.Now()
	insertQuery := `
		INSERT INTO torrent_queue (id, package_id, server_id, status, queued_at)
		VALUES ($1, $2, $3, 'queued', $4)
		ON CONFLICT (package_id, server_id) DO UPDATE SET
			status = 'queued',
			progress_percent = 0,
			error_message = NULL,
			current_file = NULL,
			started_at = NULL,
			completed_at = NULL,
			queued_at = $4
		WHERE torrent_queue.status NOT IN ('queued', 'generating')
	`

	result, err := qm.db.Exec(insertQuery, id, packageID, qm.serverID, now)
	if err != nil {
		return fmt.Errorf("failed to add to queue: %w", err)
	}

	affected, _ := result.RowsAffected()
	if affected > 0 {
		log.Printf("Added/re-queued package %s for torrent generation", packageID)
	}
	return nil
}

// GetQueueStatus returns the current queue status
func (qm *QueueManager) GetQueueStatus() (map[string]int, error) {
	query := `
		SELECT status, COUNT(*) as count
		FROM torrent_queue
		WHERE server_id = $1
		GROUP BY status
	`

	rows, err := qm.db.Query(query, qm.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	status := make(map[string]int)
	for rows.Next() {
		var s string
		var count int
		if err := rows.Scan(&s, &count); err != nil {
			return nil, err
		}
		status[s] = count
	}

	return status, nil
}

// RetryFailed retries all failed queue items
func (qm *QueueManager) RetryFailed() error {
	query := `
		UPDATE torrent_queue
		SET status = 'queued', error_message = NULL, progress_percent = 0
		WHERE server_id = $1 AND status = 'failed'
	`

	result, err := qm.db.Exec(query, qm.serverID)
	if err != nil {
		return err
	}

	affected, _ := result.RowsAffected()
	log.Printf("Retrying %d failed torrent generation tasks", affected)

	return nil
}

// ClearCompleted removes completed queue items
func (qm *QueueManager) ClearCompleted() error {
	query := `
		DELETE FROM torrent_queue
		WHERE server_id = $1 AND status = 'completed'
	`

	result, err := qm.db.Exec(query, qm.serverID)
	if err != nil {
		return err
	}

	affected, _ := result.RowsAffected()
	log.Printf("Cleared %d completed torrent generation tasks", affected)

	return nil
}

// uploadPendingTorrents uploads all locally-generated torrents to the main server.
// This runs at startup to handle torrents that were generated before the upload feature
// was added, or where a previous upload attempt failed.
func (qm *QueueManager) uploadPendingTorrents() {
	log.Printf("[torrent-upload] Checking for locally-generated torrents to upload to main server...")

	// Get all torrents generated by this server
	query := `
		SELECT dt.info_hash, dt.package_id, dt.torrent_file, dt.piece_size, dt.total_pieces,
		       dt.file_count, dt.total_size_bytes, p.assetmap_uuid, p.package_name
		FROM dcp_torrents dt
		JOIN dcp_packages p ON p.id = dt.package_id
		WHERE dt.created_by_server_id = $1
	`

	rows, err := qm.db.Query(query, qm.serverID)
	if err != nil {
		log.Printf("[torrent-upload] Error querying local torrents: %v", err)
		return
	}
	defer rows.Close()

	type localTorrent struct {
		infoHash       string
		packageID      string
		torrentFile    []byte
		pieceSize      int
		totalPieces    int
		fileCount      int
		totalSizeBytes int64
		assetMapUUID   string
		packageName    string
	}

	var torrents []localTorrent
	for rows.Next() {
		var t localTorrent
		if err := rows.Scan(&t.infoHash, &t.packageID, &t.torrentFile, &t.pieceSize, &t.totalPieces,
			&t.fileCount, &t.totalSizeBytes, &t.assetMapUUID, &t.packageName); err != nil {
			log.Printf("[torrent-upload] Error scanning torrent row: %v", err)
			continue
		}
		torrents = append(torrents, t)
	}

	if len(torrents) == 0 {
		log.Printf("[torrent-upload] No locally-generated torrents found to upload")
		return
	}

	log.Printf("[torrent-upload] Found %d locally-generated torrents, uploading to main server...", len(torrents))

	uploaded := 0
	failed := 0
	for _, t := range torrents {
		payload := RegisterTorrentRequest{
			AssetMapUUID:   t.assetMapUUID,
			InfoHash:       t.infoHash,
			TorrentFile:    t.torrentFile,
			PieceSize:      t.pieceSize,
			TotalPieces:    t.totalPieces,
			FileCount:      t.fileCount,
			TotalSizeBytes: t.totalSizeBytes,
			ServerID:       qm.remoteServerID,
		}

		jsonData, err := json.Marshal(payload)
		if err != nil {
			log.Printf("[torrent-upload] Failed to marshal torrent %s: %v", t.infoHash, err)
			failed++
			continue
		}

		url := fmt.Sprintf("%s/api/v1/torrents", qm.mainServerURL)
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("[torrent-upload] Failed to create request for %s: %v", t.infoHash, err)
			failed++
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Server-ID", qm.remoteServerID)
		req.Header.Set("X-MAC-Address", qm.macAddress)

		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[torrent-upload] Failed to upload torrent %s (%s): %v", t.packageName, t.infoHash, err)
			failed++
			continue
		}
		respBody, _ := ioutil.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			uploaded++
			if uploaded%10 == 0 || uploaded == len(torrents) {
				log.Printf("[torrent-upload] Progress: %d/%d torrents uploaded to main server", uploaded, len(torrents))
			}
		} else {
			log.Printf("[torrent-upload] Main server rejected torrent %s (%s): status %d: %s",
				t.packageName, t.infoHash, resp.StatusCode, string(respBody))
			failed++
		}
	}

	log.Printf("[torrent-upload] Upload complete: %d uploaded, %d failed out of %d total", uploaded, failed, len(torrents))
}

// syncMissingTorrents asks the main server which packages in our inventory are missing
// torrents, then checks if we have those torrents locally and uploads them.
// This ensures the main server always has every torrent that any client has generated.
func (qm *QueueManager) syncMissingTorrents() {
	// Ask the main server for packages we have in inventory but it has no torrent for
	url := fmt.Sprintf("%s/api/v1/servers/%s/missing-torrents", qm.mainServerURL, qm.remoteServerID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("[torrent-sync] Failed to create missing-torrents request: %v", err)
		return
	}
	req.Header.Set("X-Server-ID", qm.remoteServerID)
	req.Header.Set("X-MAC-Address", qm.macAddress)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[torrent-sync] Failed to fetch missing torrents from main server: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		log.Printf("[torrent-sync] Main server returned status %d: %s", resp.StatusCode, string(body))
		return
	}

	var result struct {
		MissingUUIDs []string `json:"missing_assetmap_uuids"`
		Count        int      `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[torrent-sync] Failed to decode missing-torrents response: %v", err)
		return
	}

	if result.Count == 0 {
		return // All packages have torrents on main server
	}

	log.Printf("[torrent-sync] Main server is missing torrents for %d packages in our inventory, checking local DB...", result.Count)

	// For each missing UUID, check if we have the torrent locally
	uploaded := 0
	for _, assetMapUUID := range result.MissingUUIDs {
		// Look up local package by assetmap_uuid
		var packageID string
		err := qm.db.QueryRow("SELECT id FROM dcp_packages WHERE assetmap_uuid = $1", assetMapUUID).Scan(&packageID)
		if err != nil {
			continue // Package not in our local DB
		}

		// Check if we have a torrent for this package
		var infoHash string
		var torrentFile []byte
		var pieceSize, totalPieces, fileCount int
		var totalSizeBytes int64
		err = qm.db.QueryRow(`
			SELECT info_hash, torrent_file, piece_size, total_pieces, file_count, total_size_bytes
			FROM dcp_torrents WHERE package_id = $1
		`, packageID).Scan(&infoHash, &torrentFile, &pieceSize, &totalPieces, &fileCount, &totalSizeBytes)
		if err != nil {
			continue // No local torrent for this package
		}

		// We have it locally - upload to main server
		log.Printf("[torrent-sync] Found local torrent for %s (info_hash=%s), uploading to main server", assetMapUUID, infoHash)

		payload := RegisterTorrentRequest{
			AssetMapUUID:   assetMapUUID,
			InfoHash:       infoHash,
			TorrentFile:    torrentFile,
			PieceSize:      pieceSize,
			TotalPieces:    totalPieces,
			FileCount:      fileCount,
			TotalSizeBytes: totalSizeBytes,
			ServerID:       qm.remoteServerID,
		}

		jsonData, err := json.Marshal(payload)
		if err != nil {
			log.Printf("[torrent-sync] Failed to marshal torrent %s: %v", infoHash, err)
			continue
		}

		uploadURL := fmt.Sprintf("%s/api/v1/torrents", qm.mainServerURL)
		uploadReq, err := http.NewRequest("POST", uploadURL, bytes.NewBuffer(jsonData))
		if err != nil {
			continue
		}
		uploadReq.Header.Set("Content-Type", "application/json")
		uploadReq.Header.Set("X-Server-ID", qm.remoteServerID)
		uploadReq.Header.Set("X-MAC-Address", qm.macAddress)

		uploadResp, err := client.Do(uploadReq)
		if err != nil {
			log.Printf("[torrent-sync] Failed to upload torrent %s: %v", infoHash, err)
			continue
		}
		uploadResp.Body.Close()

		if uploadResp.StatusCode == http.StatusOK || uploadResp.StatusCode == http.StatusCreated {
			uploaded++
			log.Printf("[torrent-sync] Uploaded missing torrent for %s (info_hash=%s) to main server", assetMapUUID, infoHash)
		} else {
			log.Printf("[torrent-sync] Main server rejected torrent %s: status %d", infoHash, uploadResp.StatusCode)
		}
	}

	if uploaded > 0 {
		log.Printf("[torrent-sync] Synced %d missing torrents to main server", uploaded)
	} else {
		log.Printf("[torrent-sync] No local torrents found for the %d packages missing on main server", result.Count)
	}
}
