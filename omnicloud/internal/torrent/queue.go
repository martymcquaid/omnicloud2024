package torrent

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/google/uuid"
)

// QueueManager manages the torrent generation queue
type QueueManager struct {
	db         *sql.DB
	generator  *Generator
	client     *Client
	serverID   string
	maxWorkers int
	
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
	}
}

// Start starts the queue processor. Only one process per (database, server_id) should run this;
// on the main server there is a single process; on client sites each runs its own process with its own server_id.
func (qm *QueueManager) Start(ctx context.Context) {
	log.Printf("Starting torrent queue manager (server_id=%s pid=%d)...", qm.serverID, os.Getpid())

	// Reset any stale "generating" items back to "queued" on startup
	// This handles items that were marked as generating but system crashed/restarted
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

// processQueue checks for queued items and processes them
func (qm *QueueManager) processQueue(ctx context.Context) {
	// Mark any queued items that can never be picked (no inventory path) as failed
	qm.markUnpickableQueuedItemsAsFailed()

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

// getNextQueuedItem retrieves the next queued item from database.
// Requires a matching server_dcp_inventory row so we have local_path for hashing.
// Queued items without inventory are never returned here; they are marked failed by markUnpickableQueuedItemsAsFailed.
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

// AddToQueue adds a package to the torrent generation queue
func (qm *QueueManager) AddToQueue(packageID string) error {
	// Check if already in queue
	var exists bool
	checkQuery := `
		SELECT EXISTS(SELECT 1 FROM torrent_queue WHERE package_id = $1 AND server_id = $2 AND status IN ('queued', 'generating'))
	`
	err := qm.db.QueryRow(checkQuery, packageID, qm.serverID).Scan(&exists)
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
