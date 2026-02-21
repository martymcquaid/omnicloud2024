package torrent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/google/uuid"
)

// QueueManager manages the torrent generation queue
type QueueManager struct {
	db             *sql.DB
	generator      *Generator
	client         *Client
	serverID       string
	maxWorkers     int
	mainServerURL  string // if set (client mode), check main before adding so we don't hash what another server is already hashing
	httpClient     *http.Client
	
	mu         sync.Mutex
	workers    int
	stopChan   chan struct{}
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
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetMainServerURL sets the main server URL for client mode. When set, AddToQueue will ask the main
// server if this package is already being hashed by any server; if so, we don't add (wait for torrent to be ready instead).
func (qm *QueueManager) SetMainServerURL(url string) {
	qm.mainServerURL = strings.TrimSuffix(url, "/")
}

// Start starts the queue processor
func (qm *QueueManager) Start(ctx context.Context) {
	log.Printf("Starting torrent queue manager (server_id=%s)...", qm.serverID)

	// Reset any stale "generating" items and adopt orphaned ones from other server_ids
	qm.resetStaleGeneratingItems()

	ticker := time.NewTicker(10 * time.Second)
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

// resetStaleGeneratingItems resets items stuck in "generating" and adopts orphaned rows from other server_ids.
func (qm *QueueManager) resetStaleGeneratingItems() {
	log.Printf("Queue startup: cleaning up stale queue items (server_id=%s)", qm.serverID)

	// 0) Delete ALL completed items - if torrent was successfully created, we don't need queue entries from any server
	deleteCompleted := `
		DELETE FROM torrent_queue
		WHERE status = 'completed'
	`
	if result, err := qm.db.Exec(deleteCompleted); err != nil {
		log.Printf("Error deleting completed items: %v", err)
	} else if affected, _ := result.RowsAffected(); affected > 0 {
		log.Printf("Deleted %d completed queue items from all servers (cleanup)", affected)
	}

	// 1) Reset current server's generating items to queued (interrupted/crashed mid-generation)
	query := `
		UPDATE torrent_queue
		SET status = 'queued', progress_percent = 0, current_file = NULL, started_at = NULL
		WHERE server_id = $1 AND status = 'generating'
	`
	result, err := qm.db.Exec(query, qm.serverID)
	if err != nil {
		log.Printf("Error resetting stale generating items: %v", err)
		return
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		log.Printf("Reset %d stale 'generating' items back to 'queued' status", affected)
	}

	// 2) Reset failed items to queued (allow retry on restart)
	resetFailed := `
		UPDATE torrent_queue
		SET status = 'queued', progress_percent = 0, error_message = NULL, current_file = NULL, started_at = NULL
		WHERE server_id = $1 AND status = 'failed'
	`
	if result, err := qm.db.Exec(resetFailed, qm.serverID); err != nil {
		log.Printf("Error resetting failed items: %v", err)
	} else if affected, _ := result.RowsAffected(); affected > 0 {
		log.Printf("Reset %d failed items to 'queued' for retry", affected)
	}

	// 3) Deduplicate then adopt generating items from other server_ids
	delQuery := `
		DELETE FROM torrent_queue a USING torrent_queue b
		WHERE a.status = 'generating' AND a.server_id != $1
		  AND b.status = 'generating' AND b.server_id != $1
		  AND a.package_id = b.package_id AND a.id > b.id
	`
	if _, err := qm.db.Exec(delQuery, qm.serverID); err != nil {
		log.Printf("Error deduplicating stale generating items: %v", err)
		return
	}
	adoptQuery := `
		UPDATE torrent_queue
		SET server_id = $1, status = 'queued', progress_percent = 0, current_file = NULL, started_at = NULL
		WHERE status = 'generating' AND server_id != $1
	`
	adoptResult, err := qm.db.Exec(adoptQuery, qm.serverID)
	if err != nil {
		log.Printf("Error adopting stale generating items: %v", err)
		return
	}
	if adopted, _ := adoptResult.RowsAffected(); adopted > 0 {
		log.Printf("Adopted %d stale 'generating' items from other servers to this server (now queued)", adopted)
	}
}

// processQueue checks for queued items and processes them
func (qm *QueueManager) processQueue(ctx context.Context) {
	// Check if we have capacity
	qm.mu.Lock()
	currentWorkers := qm.workers
	qm.mu.Unlock()
	
	if currentWorkers >= qm.maxWorkers {
		// Workers busy (e.g. hashing large DCPs); skip logging to avoid spam every 10s
		return
	}

	// Get next queued item
	item, err := qm.getNextQueuedItem()
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("Error getting queued item: %v", err)
		}
		return
	}

	// ALL servers: Check if ANY server is currently GENERATING (actively hashing) this package
	// Don't skip if they're just queued - allow first one to start hashing
	var otherServerGenerating bool
	crossServerCheck := `
		SELECT EXISTS(SELECT 1 FROM torrent_queue WHERE package_id = $1 AND server_id != $2 AND status = 'generating')
	`
	err = qm.db.QueryRow(crossServerCheck, item.PackageID, qm.serverID).Scan(&otherServerGenerating)
	if err != nil {
		log.Printf("Could not check if other servers are hashing package %s: %v (will start worker)", item.PackageID, err)
	} else if otherServerGenerating {
		log.Printf("Package %s is already being hashed by another server; skipping worker start (will use torrent when ready)", item.PackageID)
		return // leave item queued; will recheck next tick
	}

	// Client mode: don't start if another server is already hashing, or torrent already exists on main
	if qm.mainServerURL != "" {
		inProgress, exists, checkErr := qm.checkMainServerPackageStatus(item.PackageID)
		if checkErr == nil {
			if inProgress {
				return // leave item queued; will recheck next tick
			}
			if exists {
				_, _ = qm.db.Exec("DELETE FROM torrent_queue WHERE id = $1", item.ID)
				log.Printf("Package %s already has torrent on main; removed from local queue (use transfer to get it)", item.PackageID)
				return
			}
		}
	}

	log.Printf("Queue processor: found item %s, starting worker (%d/%d)", item.PackageName, currentWorkers+1, qm.maxWorkers)

	// Increment worker count
	qm.mu.Lock()
	qm.workers++
	qm.mu.Unlock()

	// Process in goroutine
	go func() {
		defer func() {
			qm.mu.Lock()
			qm.workers--
			qm.mu.Unlock()
		}()

		qm.processItem(ctx, item)
	}()
}

// getNextQueuedItem retrieves the next queued item from database
func (qm *QueueManager) getNextQueuedItem() (*QueueItem, error) {
	query := `
		SELECT tq.id, tq.package_id, inv.local_path, dp.package_name, tq.status, tq.queued_at
		FROM torrent_queue tq
		JOIN dcp_packages dp ON tq.package_id = dp.id
		JOIN server_dcp_inventory inv ON inv.package_id = tq.package_id AND inv.server_id = tq.server_id
		WHERE tq.server_id = $1 AND tq.status = 'queued'
		ORDER BY tq.queued_at ASC
		LIMIT 1
	`

	item := &QueueItem{}
	err := qm.db.QueryRow(query, qm.serverID).Scan(
		&item.ID, &item.PackageID, &item.PackagePath, &item.PackageName,
		&item.Status, &item.QueuedAt,
	)

	if err != nil {
		return nil, err
	}

	return item, nil
}

// processItem generates torrent for a queue item
func (qm *QueueManager) processItem(ctx context.Context, item *QueueItem) {
	log.Printf("Processing torrent generation for package: %s (%s)", item.PackageName, item.PackageID)

	// Calculate total package size before generation (for speed calculation during hashing)
	totalSize := int64(0)
	filepath.Walk(item.PackagePath, func(path string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() == false {
			totalSize += info.Size()
		}
		return nil
	})
	if totalSize > 0 {
		_, _ = qm.db.Exec("UPDATE torrent_queue SET total_size_bytes = $1 WHERE id = $2", totalSize, item.ID)
	}

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

	// Update queue item with total size for hashing speed calculation
	info, err := mi.UnmarshalInfo()
	if err == nil {
		totalSize := info.TotalLength()
		_, _ = qm.db.Exec("UPDATE torrent_queue SET total_size_bytes = $1 WHERE id = $2", totalSize, item.ID)
	}

	// Get torrent ID from database
	torrentID, err := qm.getTorrentID(infoHash)
	if err != nil {
		log.Printf("Failed to get torrent ID for %s: %v", item.PackageName, err)
		return
	}

	// Marshal torrent for seeding
	var buf bytes.Buffer
	err = bencode.NewEncoder(&buf).Encode(mi)
	if err != nil {
		log.Printf("Failed to marshal torrent for seeding %s: %v", item.PackageName, err)
		return
	}
	torrentBytes := buf.Bytes()

	// Start seeding
	err = qm.client.StartSeeding(torrentBytes, item.PackagePath, item.PackageID, torrentID)
	if err != nil {
		log.Printf("Failed to start seeding for %s: %v", item.PackageName, err)
		// Don't fail the queue item - torrent was created successfully
	} else {
		log.Printf("Started seeding %s", item.PackageName)
	}

	log.Printf("Completed torrent generation and seeding for %s", item.PackageName)
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

// checkMainServerPackageStatus returns (alreadyInProgress, torrentExists, error) from main server.
func (qm *QueueManager) checkMainServerPackageStatus(packageID string) (alreadyInProgress, torrentExists bool, err error) {
	checkURL := fmt.Sprintf("%s/api/v1/torrent-queue/check?package_id=%s", qm.mainServerURL, packageID)
	resp, err := qm.httpClient.Get(checkURL)
	if err != nil {
		return false, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, false, fmt.Errorf("check returned %d", resp.StatusCode)
	}
	var out struct {
		AlreadyInProgress bool `json:"already_in_progress"`
		TorrentExists     bool `json:"torrent_exists"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, false, err
	}
	return out.AlreadyInProgress, out.TorrentExists, nil
}

// AddToQueue adds a package to the torrent generation queue.
// When mainServerURL is set (client mode), we first ask the main server if this package is already
// queued or being hashed by any server; if so, we skip adding so the client waits for the torrent to be ready.
func (qm *QueueManager) AddToQueue(packageID string) error {
	// If we're a client, don't add if another server is already hashing or torrent already exists on main
	if qm.mainServerURL != "" {
		inProgress, exists, err := qm.checkMainServerPackageStatus(packageID)
		if err != nil {
			log.Printf("Could not check main server for package %s (will add to queue): %v", packageID, err)
		} else {
			if inProgress {
				log.Printf("Package %s is already being hashed by another server; skipping queue (will use torrent when ready)", packageID)
				return nil
			}
			if exists {
				log.Printf("Package %s already has a torrent on main server; skipping queue (use transfer or sync to get it)", packageID)
				return nil
			}
		}
	}

	// ALL servers: Check if ANY server is currently GENERATING (actively hashing) this package
	// Don't skip if they're just queued - allow first one to start hashing
	var otherServerGenerating bool
	crossServerCheck := `
		SELECT EXISTS(SELECT 1 FROM torrent_queue WHERE package_id = $1 AND server_id != $2 AND status = 'generating')
	`
	err := qm.db.QueryRow(crossServerCheck, packageID, qm.serverID).Scan(&otherServerGenerating)
	if err != nil {
		log.Printf("Could not check if other servers are hashing package %s: %v (will add to queue)", packageID, err)
	} else if otherServerGenerating {
		log.Printf("Package %s is already being hashed by another server; skipping queue (will use torrent when ready)", packageID)
		return nil
	}

	// Check if already in queue (for this server)
	var exists bool
	checkQuery := `
		SELECT EXISTS(SELECT 1 FROM torrent_queue WHERE package_id = $1 AND server_id = $2 AND status IN ('queued', 'generating'))
	`
	err = qm.db.QueryRow(checkQuery, packageID, qm.serverID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check queue: %w", err)
	}

	if exists {
		return nil // Already queued
	}

	// Add to queue
	id := uuid.New().String()
	insertQuery := `
		INSERT INTO torrent_queue (id, package_id, server_id, status, queued_at)
		VALUES ($1, $2, $3, 'queued', $4)
		ON CONFLICT (package_id, server_id) DO NOTHING
	`

	_, err = qm.db.Exec(insertQuery, id, packageID, qm.serverID, time.Now())
	if err != nil {
		return fmt.Errorf("failed to add to queue: %w", err)
	}

	log.Printf("Added package %s to torrent generation queue", packageID)
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
