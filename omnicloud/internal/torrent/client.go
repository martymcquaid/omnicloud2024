package torrent

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/google/uuid"
)

// Client manages torrent seeding and downloading
type Client struct {
	client   *torrent.Client
	db       *sql.DB
	serverID string
	
	// Track active torrents
	mu       sync.RWMutex
	torrents map[string]*ActiveTorrent // key: info_hash
}

// ActiveTorrent represents a torrent being seeded or downloaded
type ActiveTorrent struct {
	Torrent     *torrent.Torrent
	InfoHash    string
	PackageID   string
	LocalPath   string
	IsSeeding   bool
	IsDownloading bool
	AddedAt     time.Time
}

// TorrentStats contains statistics for a torrent
type TorrentStats struct {
	InfoHash         string
	BytesCompleted   int64
	BytesTotal       int64
	DownloadSpeed    int64 // bytes/sec
	UploadSpeed      int64 // bytes/sec
	PeersConnected   int
	PeersTotal       int
	Progress         float64
	IsSeeding        bool
	IsDownloading    bool
	ETA              int // seconds
}

// NewClient creates a new torrent client
func NewClient(cfg *torrent.ClientConfig, db *sql.DB, serverID string) (*Client, error) {
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create torrent client: %w", err)
	}

	return &Client{
		client:   cl,
		db:       db,
		serverID: serverID,
		torrents: make(map[string]*ActiveTorrent),
	}, nil
}

// Close closes the torrent client and all active torrents
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Close all torrents
	for _, at := range c.torrents {
		at.Torrent.Drop()
	}

	// Close client
	c.client.Close()
	return nil
}

// StartSeeding starts seeding a DCP from a torrent file
func (c *Client) StartSeeding(torrentBytes []byte, dataPath, packageID, torrentID string) error {
	// Parse torrent metainfo
	var mi metainfo.MetaInfo
	err := bencode.Unmarshal(torrentBytes, &mi)
	if err != nil {
		return fmt.Errorf("failed to parse torrent: %w", err)
	}

	infoHash := mi.HashInfoBytes().HexString()

	// Check if already seeding
	c.mu.RLock()
	if _, exists := c.torrents[infoHash]; exists {
		c.mu.RUnlock()
		return nil // Already seeding
	}
	c.mu.RUnlock()

	// Add torrent
	t, _, err := c.client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoBytes: mi.InfoBytes,
		Trackers:  [][]string{{mi.Announce}},
	})
	if err != nil {
		return fmt.Errorf("failed to add torrent: %w", err)
	}

	// Wait for torrent info (should be immediate since we have .torrent file)
	<-t.GotInfo()

	// Store active torrent
	c.mu.Lock()
	c.torrents[infoHash] = &ActiveTorrent{
		Torrent:   t,
		InfoHash:  infoHash,
		PackageID: packageID,
		LocalPath: dataPath,
		IsSeeding: true,
		AddedAt:   time.Now(),
	}
	c.mu.Unlock()

	// Register as seeder in database
	return c.registerSeeder(torrentID, dataPath, "seeding")
}

// StartDownload starts downloading a DCP via torrent
func (c *Client) StartDownload(torrentBytes []byte, destPath, packageID, transferID string) error {
	// Parse torrent metainfo
	var mi metainfo.MetaInfo
	err := bencode.Unmarshal(torrentBytes, &mi)
	if err != nil {
		return fmt.Errorf("failed to parse torrent: %w", err)
	}

	infoHash := mi.HashInfoBytes().HexString()

	// Check if already downloading/seeding
	c.mu.RLock()
	if _, exists := c.torrents[infoHash]; exists {
		c.mu.RUnlock()
		return nil // Already active
	}
	c.mu.RUnlock()

	// Add torrent
	t, _, err := c.client.AddTorrentSpec(&torrent.TorrentSpec{
		InfoBytes: mi.InfoBytes,
		Trackers:  [][]string{{mi.Announce}},
	})
	if err != nil {
		return fmt.Errorf("failed to add torrent: %w", err)
	}

	// Wait for torrent info
	<-t.GotInfo()

	// Download all files
	t.DownloadAll()

	// Store active torrent
	c.mu.Lock()
	c.torrents[infoHash] = &ActiveTorrent{
		Torrent:       t,
		InfoHash:      infoHash,
		PackageID:     packageID,
		LocalPath:     destPath,
		IsDownloading: true,
		AddedAt:       time.Now(),
	}
	c.mu.Unlock()

	// Start monitoring download in background
	go c.monitorDownload(infoHash, transferID)

	return nil
}

// monitorDownload monitors download progress and updates transfer status
func (c *Client) monitorDownload(infoHash, transferID string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.RLock()
		at, exists := c.torrents[infoHash]
		c.mu.RUnlock()

		if !exists {
			return // Torrent removed
		}

		stats := c.getTorrentStats(at.Torrent)

		// Update transfer in database
		err := c.updateTransferProgress(transferID, stats)
		if err != nil {
			fmt.Printf("Error updating transfer progress: %v\n", err)
		}

		// Check if completed
		if stats.Progress >= 100 {
			c.updateTransferStatus(transferID, "completed")
			
			// Convert to seeding
			c.mu.Lock()
			at.IsDownloading = false
			at.IsSeeding = true
			c.mu.Unlock()
			
			return
		}
	}
}

// GetStats returns statistics for a torrent by info hash
func (c *Client) GetStats(infoHash string) *TorrentStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	at, exists := c.torrents[infoHash]
	if !exists {
		return nil
	}

	return c.getTorrentStats(at.Torrent)
}

// getTorrentStats extracts statistics from a torrent
func (c *Client) getTorrentStats(t *torrent.Torrent) *TorrentStats {
	info := t.Info()

	var bytesTotal int64
	if info != nil {
		bytesTotal = info.TotalLength()
	}

	bytesCompleted := t.BytesCompleted()
	progress := 0.0
	if bytesTotal > 0 {
		progress = float64(bytesCompleted) / float64(bytesTotal) * 100
	}

	// Get connected peers count
	peersConnected := len(t.PeerConns())

	// Calculate ETA (simplified for v1.15.2)
	eta := 0
	// Note: v1.15.2 doesn't expose download speed stats easily
	// We can implement rate tracking separately if needed

	return &TorrentStats{
		InfoHash:       t.InfoHash().HexString(),
		BytesCompleted: bytesCompleted,
		BytesTotal:     bytesTotal,
		DownloadSpeed:  0, // Not available in v1.15.2 without custom tracking
		UploadSpeed:    0, // Not available in v1.15.2 without custom tracking
		PeersConnected: peersConnected,
		PeersTotal:     peersConnected,
		Progress:       progress,
		ETA:            eta,
	}
}

// GetAllStats returns statistics for all active torrents
func (c *Client) GetAllStats() map[string]*TorrentStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[string]*TorrentStats)
	for hash, at := range c.torrents {
		stats := c.getTorrentStats(at.Torrent)
		stats.IsSeeding = at.IsSeeding
		stats.IsDownloading = at.IsDownloading
		result[hash] = stats
	}

	return result
}

// StopTorrent stops seeding or downloading a torrent
func (c *Client) StopTorrent(infoHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	at, exists := c.torrents[infoHash]
	if !exists {
		return fmt.Errorf("torrent not found: %s", infoHash)
	}

	// Drop torrent
	at.Torrent.Drop()

	// Remove from active torrents
	delete(c.torrents, infoHash)

	return nil
}

// registerSeeder registers this server as a seeder in the database
func (c *Client) registerSeeder(torrentID, localPath, status string) error {
	query := `
		INSERT INTO torrent_seeders (id, torrent_id, server_id, local_path, status, last_announce, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (torrent_id, server_id) 
		DO UPDATE SET local_path = $4, status = $5, last_announce = $6, updated_at = $8
	`

	id := uuid.New().String()
	now := time.Now()

	_, err := c.db.Exec(query, id, torrentID, c.serverID, localPath, status, now, now, now)
	return err
}

// updateTransferProgress updates transfer progress in database
func (c *Client) updateTransferProgress(transferID string, stats *TorrentStats) error {
	query := `
		UPDATE transfers
		SET progress_percent = $1,
		    downloaded_bytes = $2,
		    download_speed_bps = $3,
		    upload_speed_bps = $4,
		    peers_connected = $5,
		    eta_seconds = $6,
		    updated_at = $7
		WHERE id = $8
	`

	_, err := c.db.Exec(query, stats.Progress, stats.BytesCompleted, stats.DownloadSpeed,
		stats.UploadSpeed, stats.PeersConnected, stats.ETA, time.Now(), transferID)

	return err
}

// updateTransferStatus updates transfer status
func (c *Client) updateTransferStatus(transferID, status string) error {
	var completedAt sql.NullTime
	var startedAt sql.NullTime

	if status == "completed" {
		completedAt.Time = time.Now()
		completedAt.Valid = true
	}
	
	if status == "downloading" {
		startedAt.Time = time.Now()
		startedAt.Valid = true
	}

	query := `
		UPDATE transfers
		SET status = $1,
		    completed_at = COALESCE($2, completed_at),
		    started_at = COALESCE($3, started_at),
		    updated_at = $4
		WHERE id = $5
	`

	_, err := c.db.Exec(query, status, completedAt, startedAt, time.Now(), transferID)
	return err
}

// StartStatsReporter starts a background goroutine that reports stats to main server
func (c *Client) StartStatsReporter(ctx context.Context, reportFunc func(map[string]*TorrentStats) error) {
	ticker := time.NewTicker(60 * time.Second) // Report every 60 seconds for seeding
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := c.GetAllStats()
			if len(stats) > 0 {
				if err := reportFunc(stats); err != nil {
					fmt.Printf("Error reporting stats: %v\n", err)
				}
			}
		}
	}
}
