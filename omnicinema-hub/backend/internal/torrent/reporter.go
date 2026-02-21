package torrent

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// StatusReporter handles periodic reporting of torrent status to main server
type StatusReporter struct {
	client        *Client
	db            *sql.DB
	mainServerURL string
	serverID      string
	httpClient    *http.Client
}

// TorrentStatusReport represents the status report sent to main server
type TorrentStatusReport struct {
	ServerID   string              `json:"server_id"`
	Timestamp  time.Time           `json:"timestamp"`
	Torrents   []TorrentStatusItem `json:"torrents"`
	QueueItems []QueueStatusItem   `json:"queue_items,omitempty"`
	QueueStats map[string]int      `json:"queue_stats,omitempty"`
}

// QueueStatusItem represents status for a single hashing queue item (sent to main server)
type QueueStatusItem struct {
	ID              string     `json:"id"`
	PackageID       string     `json:"package_id"`
	Status          string     `json:"status"`
	ProgressPercent float64    `json:"progress_percent"`
	CurrentFile     string     `json:"current_file,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	TotalSizeBytes  int64      `json:"total_size_bytes,omitempty"`
	HashingSpeedBps int64      `json:"hashing_speed_bps,omitempty"`
}

// TorrentStatusItem represents status for a single torrent
type TorrentStatusItem struct {
	InfoHash         string  `json:"info_hash"`
	Status           string  `json:"status"` // seeding, downloading, completed
	BytesCompleted   int64   `json:"bytes_completed"`
	BytesTotal       int64   `json:"bytes_total"`
	Progress         float64 `json:"progress"`
	DownloadSpeed    int64   `json:"download_speed_bps"`
	UploadSpeed      int64   `json:"upload_speed_bps"`
	UploadedBytes    int64   `json:"uploaded_bytes"`
	PeersConnected   int     `json:"peers_connected"`
	ETA              int     `json:"eta_seconds"`
}

// NewStatusReporter creates a new status reporter. db may be nil (then queue_items are not sent).
func NewStatusReporter(client *Client, db *sql.DB, mainServerURL, serverID string) *StatusReporter {
	return &StatusReporter{
		client:        client,
		db:            db,
		mainServerURL: mainServerURL,
		serverID:      serverID,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Start starts the periodic status reporting
func (sr *StatusReporter) Start(ctx context.Context) {
	log.Println("Starting torrent status reporter...")

	// Report more frequently for downloads (5s), less for seeding (60s)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sr.reportStatus(); err != nil {
				log.Printf("Error reporting status to main server: %v", err)
			}
		}
	}
}

// reportStatus collects and sends status to main server (torrents + hashing queue items)
func (sr *StatusReporter) reportStatus() error {
	stats := sr.client.GetAllStats()

	items := make([]TorrentStatusItem, 0, len(stats))
	for _, stat := range stats {
		status := "idle"
		if stat.IsDownloading {
			status = "downloading"
		} else if stat.IsSeeding {
			status = "seeding"
		}
		if stat.Progress >= 100 {
			status = "completed"
		}

		items = append(items, TorrentStatusItem{
			InfoHash:       stat.InfoHash,
			Status:         status,
			BytesCompleted: stat.BytesCompleted,
			BytesTotal:     stat.BytesTotal,
			Progress:       stat.Progress,
			DownloadSpeed:  stat.DownloadSpeed,
			UploadSpeed:    stat.UploadSpeed,
			PeersConnected: stat.PeersConnected,
			ETA:            stat.ETA,
		})
	}

	// Query hashing queue (queued/generating) to push progress to main server
	queueItems := make([]QueueStatusItem, 0)
	if sr.db != nil {
		query := `
			SELECT id, package_id, status, COALESCE(progress_percent, 0),
			       COALESCE(current_file, ''), started_at, COALESCE(total_size_bytes, 0),
			       COALESCE(hashing_speed_bps, 0)
			FROM torrent_queue
			WHERE status IN ('queued', 'generating')
			ORDER BY queued_at
		`
		rows, err := sr.db.Query(query)
		if err != nil {
			log.Printf("Error querying queue items for report: %v", err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var item QueueStatusItem
				var startedAt sql.NullTime
				err := rows.Scan(&item.ID, &item.PackageID, &item.Status, &item.ProgressPercent,
					&item.CurrentFile, &startedAt, &item.TotalSizeBytes, &item.HashingSpeedBps)
				if err != nil {
					log.Printf("Error scanning queue item: %v", err)
					continue
				}
				if startedAt.Valid {
					item.StartedAt = &startedAt.Time
				}
				queueItems = append(queueItems, item)
			}
		}
	}

	if len(items) == 0 && len(queueItems) == 0 {
		return nil
	}

	report := TorrentStatusReport{
		ServerID:   sr.serverID,
		Timestamp:  time.Now(),
		Torrents:   items,
		QueueItems: queueItems,
	}
	return sr.sendReport(report)
}

// sendReport sends the status report to main server
func (sr *StatusReporter) sendReport(report TorrentStatusReport) error {
	if sr.mainServerURL == "" {
		return nil // Not a client, skip reporting
	}

	url := fmt.Sprintf("%s/api/v1/servers/%s/torrent-status", sr.mainServerURL, sr.serverID)

	jsonData, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := sr.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	return nil
}

// ReportQueueStatus reports torrent generation queue status
func (sr *StatusReporter) ReportQueueStatus(stats map[string]int) error {
	if sr.mainServerURL == "" {
		return nil
	}

	report := TorrentStatusReport{
		ServerID:   sr.serverID,
		Timestamp:  time.Now(),
		Torrents:   []TorrentStatusItem{},
		QueueStats: stats,
	}

	return sr.sendReport(report)
}
