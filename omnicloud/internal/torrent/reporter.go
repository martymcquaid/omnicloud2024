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

// reporterSample tracks the previous raw byte counters for speed computation
type reporterSample struct {
	bytesRead    int64
	bytesWritten int64
	timestamp    time.Time
}

// StatusReporter handles periodic reporting of torrent status to main server
type StatusReporter struct {
	client        *Client
	db            *sql.DB
	serverID      string
	macAddress    string
	mainServerURL string
	httpClient    *http.Client
	prevSamples   map[string]reporterSample // per-torrent previous raw bytes for speed calc
	avgSpeeds     map[string]int64          // exponential moving average of download speed (bytes/sec)
	firstReport   bool                      // true until first successful report is sent
}

// TorrentStatusReport represents the status report sent to main server
type TorrentStatusReport struct {
	ServerID    string                 `json:"server_id"`
	Timestamp   time.Time              `json:"timestamp"`
	Torrents    []TorrentStatusItem    `json:"torrents"`
	QueueItems  []QueueStatusItem      `json:"queue_items,omitempty"`
	QueueStats  map[string]int         `json:"queue_stats,omitempty"`
	IsFullSync  bool                   `json:"is_full_sync,omitempty"`
	// NAT/relay status
	IsBehindNAT     bool `json:"is_behind_nat,omitempty"`
	RelayRegistered bool `json:"relay_registered,omitempty"`
}

// TorrentStatusItem represents status for a single torrent
type TorrentStatusItem struct {
	InfoHash            string     `json:"info_hash"`
	Status              string     `json:"status"` // verifying, seeding, downloading, completed, error
	IsLoaded            bool       `json:"is_loaded"`
	IsSeeding           bool       `json:"is_seeding"`
	IsDownloading       bool       `json:"is_downloading"`
	BytesCompleted      int64      `json:"bytes_completed"`
	BytesTotal          int64      `json:"bytes_total"`
	Progress            float64    `json:"progress"`
	PiecesCompleted     int        `json:"pieces_completed"`
	PiecesTotal         int        `json:"pieces_total"`
	DownloadSpeed       int64      `json:"download_speed_bps"`
	UploadSpeed         int64      `json:"upload_speed_bps"`
	UploadedBytes       int64      `json:"uploaded_bytes"`
	PeersConnected      int        `json:"peers_connected"`
	ETA                 int        `json:"eta_seconds"`
	ErrorMessage        string     `json:"error_message,omitempty"`
	AnnouncedToTracker  bool       `json:"announced_to_tracker"`
	LastAnnounceAttempt *time.Time `json:"last_announce_attempt,omitempty"`
	LastAnnounceSuccess *time.Time `json:"last_announce_success,omitempty"`
	AnnounceError       string     `json:"announce_error,omitempty"`
}

// QueueStatusItem represents status for a single queue item
type QueueStatusItem struct {
	ID              string     `json:"id"`
	PackageID       string     `json:"package_id"`
	AssetMapUUID    string     `json:"assetmap_uuid,omitempty"`
	Status          string     `json:"status"`
	ProgressPercent float64    `json:"progress_percent"`
	CurrentFile     string     `json:"current_file,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	TotalSizeBytes  int64      `json:"total_size_bytes,omitempty"`
	HashingSpeedBps int64      `json:"hashing_speed_bps,omitempty"`
}

// NewStatusReporter creates a new status reporter
func NewStatusReporter(client *Client, db *sql.DB, mainServerURL, serverID, macAddress string) *StatusReporter {
	return &StatusReporter{
		client:        client,
		db:            db,
		mainServerURL: mainServerURL,
		serverID:      serverID,
		macAddress:    macAddress,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		prevSamples: make(map[string]reporterSample),
		avgSpeeds:   make(map[string]int64),
		firstReport: true,
	}
}

// Start starts the periodic status reporting
func (sr *StatusReporter) Start(ctx context.Context) {
	log.Println("Starting torrent status reporter...")

	// Base tick is 5s. When no active downloads, only report every 6th tick (30s).
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	tickCount := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickCount++
			// Check if we have active downloads WITHOUT calling GetAllStats()
			// (GetAllStats corrupts speed samples because it resets the measurement baseline)
			hasDownloads := sr.client.HasActiveDownloads()
			if !hasDownloads && tickCount%6 != 0 {
				continue // Only report every 30s when seeding/idle
			}
			if err := sr.reportStatus(); err != nil {
				log.Printf("Error reporting status to main server: %v", err)
			}
		}
	}
}

// reportStatus collects and sends status to main server
func (sr *StatusReporter) reportStatus() error {
	// Get stats without touching the download monitor's speed samples
	stats := sr.client.GetAllStatsForReporter()
	now := time.Now()

	// Build torrent items
	items := make([]TorrentStatusItem, 0, len(stats))
	for _, stat := range stats {
		status := "idle"
		isLoaded := true
		announcedToTracker := false

		// Compute speed from raw cumulative bytes (reporter's own tracking)
		var downloadSpeed, uploadSpeed int64
		prev, hasPrev := sr.prevSamples[stat.InfoHash]
		if hasPrev {
			elapsed := now.Sub(prev.timestamp).Seconds()
			if elapsed > 0 {
				downloadSpeed = int64(float64(stat.rawBytesRead-prev.bytesRead) / elapsed)
				uploadSpeed = int64(float64(stat.rawBytesWritten-prev.bytesWritten) / elapsed)
				if downloadSpeed < 0 {
					downloadSpeed = 0
				}
				if uploadSpeed < 0 {
					uploadSpeed = 0
				}
			}
		}
		sr.prevSamples[stat.InfoHash] = reporterSample{
			bytesRead:    stat.rawBytesRead,
			bytesWritten: stat.rawBytesWritten,
			timestamp:    now,
		}

		// Exponential moving average for smooth speed display and accurate ETA.
		// alpha=0.3: 30% weight on new sample, 70% weight on history.
		// Smooths out bursts while still responding to sustained speed changes.
		prevAvg := sr.avgSpeeds[stat.InfoHash]
		if prevAvg == 0 && downloadSpeed > 0 {
			// First non-zero sample: use it directly
			sr.avgSpeeds[stat.InfoHash] = downloadSpeed
		} else if downloadSpeed > 0 || prevAvg > 0 {
			alpha := 0.3
			sr.avgSpeeds[stat.InfoHash] = int64(alpha*float64(downloadSpeed) + (1-alpha)*float64(prevAvg))
		}
		smoothSpeed := sr.avgSpeeds[stat.InfoHash]

		// ETA based on smoothed average speed
		eta := 0
		bytesRemaining := stat.BytesTotal - stat.BytesCompleted
		if smoothSpeed > 0 && bytesRemaining > 0 {
			eta = int(bytesRemaining / smoothSpeed)
		}

		// Determine status
		if stat.IsErrored {
			status = "error"
		} else if stat.HasTransfer {
			// This is a download (has a transfer ID)
			if stat.Progress >= 100 {
				status = "completed"
			} else if !stat.IsDownloading && stat.PeersConnected == 0 {
				// IsDownloading=false + no peers = paused by PauseTorrent()
				// (PauseTorrent sets IsDownloading=false, cancels pieces, and blocks connections)
				status = "paused"
			} else if stat.PeersConnected > 0 {
				// Peers connected = actively downloading (speed may momentarily dip to zero)
				status = "downloading"
			} else {
				// No peers yet — verifying existing data or waiting for tracker response
				status = "checking"
			}
		} else if stat.Progress < 100 {
			if stat.PeersConnected > 0 {
				if stat.IsDownloading {
					status = "downloading"
				} else {
					status = "verifying"
				}
			} else {
				status = "verifying"
			}
		} else {
			// Progress is 100%
			if stat.IsSeeding {
				status = "seeding"
				announcedToTracker = true
			} else {
				status = "completed"
			}
		}

		items = append(items, TorrentStatusItem{
			InfoHash:           stat.InfoHash,
			Status:             status,
			IsLoaded:           isLoaded,
			IsSeeding:          stat.IsSeeding,
			IsDownloading:      stat.IsDownloading,
			BytesCompleted:     stat.BytesCompleted,
			BytesTotal:         stat.BytesTotal,
			Progress:           stat.Progress,
			PiecesCompleted:    stat.PiecesCompleted,
			PiecesTotal:        stat.PiecesTotal,
			DownloadSpeed:      downloadSpeed, // true current speed (instantaneous)
			UploadSpeed:        uploadSpeed,   // true current speed (instantaneous)
			PeersConnected:     stat.PeersConnected,
			ETA:                eta,           // based on smoothed average for stability
			ErrorMessage:       stat.ErrorMessage,
			AnnouncedToTracker: announcedToTracker,
		})
	}

	// Get queue items (hashing progress) - include assetmap_uuid for cross-server matching
	// Send ALL statuses so the main server has a complete picture
	queueItems := make([]QueueStatusItem, 0)
	if sr.db != nil {
		query := `
			SELECT tq.id, tq.package_id, p.assetmap_uuid, tq.status, COALESCE(tq.progress_percent, 0),
			       COALESCE(tq.current_file, ''), tq.started_at, COALESCE(tq.total_size_bytes, 0),
			       COALESCE(tq.hashing_speed_bps, 0)
			FROM torrent_queue tq
			JOIN dcp_packages p ON p.id = tq.package_id
			ORDER BY tq.queued_at
		`
		rows, err := sr.db.Query(query)
		if err != nil {
			log.Printf("Error querying queue items: %v", err)
		} else {
			defer rows.Close()
			for rows.Next() {
				var item QueueStatusItem
				var startedAt sql.NullTime
				err := rows.Scan(&item.ID, &item.PackageID, &item.AssetMapUUID, &item.Status, &item.ProgressPercent,
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

	// Don't report if there's nothing to send (unless it's the first report for cleanup)
	if len(items) == 0 && len(queueItems) == 0 && !sr.firstReport {
		return nil
	}

	report := TorrentStatusReport{
		ServerID:   sr.serverID,
		Timestamp:  time.Now(),
		Torrents:   items,
		QueueItems: queueItems,
		IsFullSync: sr.firstReport,
	}

	// Send to main server
	err := sr.sendReport(report)
	if err == nil && sr.firstReport {
		sr.firstReport = false
		log.Printf("[reporter] First full sync sent to main server (%d torrents, %d queue items)", len(items), len(queueItems))
	}
	return err
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
	req.Header.Set("X-Server-ID", sr.serverID)
	req.Header.Set("X-MAC-Address", sr.macAddress)

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
