package websocket

import (
	"database/sql"
	"fmt"
	"time"
)

// FormatBytes returns a human-readable byte string
func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatSpeed returns a human-readable speed string from bytes/sec
func FormatSpeed(bytesPerSec int64) string {
	if bytesPerSec <= 0 {
		return ""
	}
	return FormatBytes(bytesPerSec) + "/s"
}

// NewQueueActivityCollector creates a collector that reports torrent generation queue activities
func NewQueueActivityCollector(db *sql.DB, serverID string) ActivityCollector {
	return func() []ActivityItem {
		var items []ActivityItem

		query := `
			SELECT COALESCE(p.title, tq.package_id::text), tq.status,
			       COALESCE(tq.progress_percent, 0), COALESCE(tq.current_file, ''),
			       COALESCE(tq.total_size_bytes, 0), COALESCE(tq.hashing_speed_bps, 0),
			       tq.started_at
			FROM torrent_queue tq
			LEFT JOIN dcp_packages p ON p.id = tq.package_id
			WHERE tq.server_id = $1 AND tq.status IN ('queued', 'generating')
			ORDER BY tq.queued_at
		`

		rows, err := db.Query(query, serverID)
		if err != nil {
			return items
		}
		defer rows.Close()

		queuedCount := 0
		for rows.Next() {
			var name, status, currentFile string
			var progress float64
			var totalSize, hashSpeed int64
			var startedAt sql.NullTime
			if err := rows.Scan(&name, &status, &progress, &currentFile, &totalSize, &hashSpeed, &startedAt); err != nil {
				continue
			}

			if status == "generating" {
				item := ActivityItem{
					Category:   "torrent_gen",
					Action:     "progress",
					Title:      fmt.Sprintf("Generating torrent: %s", name),
					Detail:     currentFile,
					Progress:   progress,
					SpeedBytes: hashSpeed,
					Speed:      FormatSpeed(hashSpeed),
				}
				if startedAt.Valid {
					t := startedAt.Time
					item.StartedAt = &t
				}
				if totalSize > 0 {
					item.Extra = map[string]interface{}{
						"total_size": totalSize,
					}
				}
				items = append(items, item)
			} else {
				queuedCount++
			}
		}

		if queuedCount > 0 {
			items = append(items, ActivityItem{
				Category: "torrent_gen",
				Action:   "idle",
				Title:    fmt.Sprintf("%d torrent(s) queued for generation", queuedCount),
			})
		}

		return items
	}
}

// NewTransferPendingCollector creates a collector that checks for pending transfers
func NewTransferPendingCollector(db *sql.DB, serverID string) ActivityCollector {
	return func() []ActivityItem {
		var items []ActivityItem

		var pendingCount int
		err := db.QueryRow(`
			SELECT COUNT(*) FROM transfers
			WHERE destination_server_id = $1 AND status = 'pending'
		`, serverID).Scan(&pendingCount)

		if err != nil || pendingCount == 0 {
			return items
		}

		items = append(items, ActivityItem{
			Category: "transfer",
			Action:   "idle",
			Title:    fmt.Sprintf("%d pending transfer(s)", pendingCount),
			Extra: map[string]interface{}{
				"count": pendingCount,
			},
		})

		return items
	}
}

// ScannerState tracks whether the scanner is currently running
type ScannerState struct {
	IsScanning    bool
	ScanStarted   time.Time
	LibraryPath   string
	PackagesFound int
}
