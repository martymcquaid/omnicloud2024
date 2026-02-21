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
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TransferDownloader polls the main server for pending transfers and downloads them via BitTorrent.
// It runs on client servers only.
type TransferDownloader struct {
	client        *Client
	db            *sql.DB
	mainServerURL string
	serverID      string
	downloadPath  string // base directory where DCPs are downloaded to
	httpClient    *http.Client
	maxDownloads  int

	mu     sync.Mutex
	active map[string]bool // transferID -> active (prevents double-start)
}

// PendingTransfer mirrors the response from GET /servers/{id}/pending-transfers
type PendingTransfer struct {
	ID             string `json:"id"`
	TorrentID      string `json:"torrent_id"`
	InfoHash       string `json:"info_hash"`
	PackageID      string `json:"package_id"`
	PackageName    string `json:"package_name"`
	Status         string `json:"status"`
	TotalSizeBytes int64  `json:"total_size_bytes"`
	Priority       int    `json:"priority"`
}

// NewTransferDownloader creates a new TransferDownloader.
func NewTransferDownloader(client *Client, db *sql.DB, mainServerURL, serverID, downloadPath string, maxDownloads int) *TransferDownloader {
	if maxDownloads <= 0 {
		maxDownloads = 3
	}
	return &TransferDownloader{
		client:        client,
		db:            db,
		mainServerURL: mainServerURL,
		serverID:      serverID,
		downloadPath:  downloadPath,
		maxDownloads:  maxDownloads,
		active:        make(map[string]bool),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Start begins the polling loop. It should be run in a goroutine.
func (td *TransferDownloader) Start(ctx context.Context) {
	log.Println("Transfer downloader starting (waiting for registration)...")

	// Wait for client registration to complete
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}

	log.Println("Transfer downloader active — polling for pending transfers every 15s")

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Run immediately once, then on ticker
	td.pollAndProcess(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("Transfer downloader stopping")
			return
		case <-ticker.C:
			td.pollAndProcess(ctx)
		}
	}
}

// pollAndProcess fetches pending transfers and starts downloads for new ones.
func (td *TransferDownloader) pollAndProcess(ctx context.Context) {
	transfers, err := td.fetchPendingTransfers()
	if err != nil {
		log.Printf("Transfer downloader: error fetching pending transfers: %v", err)
		return
	}

	if len(transfers) == 0 {
		return
	}

	td.mu.Lock()
	activeCount := len(td.active)
	td.mu.Unlock()

	for _, t := range transfers {
		// Check context
		select {
		case <-ctx.Done():
			return
		default:
		}

		td.mu.Lock()
		alreadyActive := td.active[t.ID]
		td.mu.Unlock()

		if alreadyActive {
			continue // already downloading
		}

		if activeCount >= td.maxDownloads {
			log.Printf("Transfer downloader: at max concurrent downloads (%d), skipping remaining", td.maxDownloads)
			break
		}

		// Start download for this transfer
		log.Printf("Transfer downloader: starting download for %q (transfer=%s, info_hash=%s)", t.PackageName, t.ID, t.InfoHash)

		if err := td.startTransferDownload(ctx, t); err != nil {
			log.Printf("Transfer downloader: failed to start download for %s: %v", t.PackageName, err)
			td.updateTransferOnMain(t.ID, "failed", err.Error())
			continue
		}

		td.mu.Lock()
		td.active[t.ID] = true
		td.mu.Unlock()
		activeCount++
	}
}

// fetchPendingTransfers calls the main server to get transfers assigned to this server.
func (td *TransferDownloader) fetchPendingTransfers() ([]PendingTransfer, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/pending-transfers", td.mainServerURL, td.serverID)

	resp, err := td.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var transfers []PendingTransfer
	if err := json.NewDecoder(resp.Body).Decode(&transfers); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return transfers, nil
}

// fetchTorrentFile downloads the .torrent file bytes from the main server.
func (td *TransferDownloader) fetchTorrentFile(infoHash string) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/torrents/%s/file", td.mainServerURL, infoHash)

	resp, err := td.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return data, nil
}

// startTransferDownload downloads the torrent file and begins the BitTorrent download.
func (td *TransferDownloader) startTransferDownload(ctx context.Context, transfer PendingTransfer) error {
	// 1. Download .torrent file from main server
	torrentBytes, err := td.fetchTorrentFile(transfer.InfoHash)
	if err != nil {
		return fmt.Errorf("failed to fetch torrent file: %w", err)
	}

	if len(torrentBytes) == 0 {
		return fmt.Errorf("empty torrent file received")
	}

	log.Printf("Transfer downloader: downloaded .torrent file for %s (%d bytes)", transfer.PackageName, len(torrentBytes))

	// 2. Update transfer status to "downloading" on main server
	td.updateTransferOnMain(transfer.ID, "downloading", "")

	// 3. Start the BitTorrent download
	err = td.client.StartDownload(torrentBytes, td.downloadPath, transfer.PackageID, transfer.ID)
	if err != nil {
		return fmt.Errorf("failed to start torrent download: %w", err)
	}

	log.Printf("Transfer downloader: BitTorrent download started for %s", transfer.PackageName)

	// 4. Start a completion watcher goroutine
	go td.watchCompletion(ctx, transfer)

	return nil
}

// watchCompletion monitors the download and handles completion.
func (td *TransferDownloader) watchCompletion(ctx context.Context, transfer PendingTransfer) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := td.client.GetStats(transfer.InfoHash)
			if stats == nil {
				// Torrent may have been removed
				log.Printf("Transfer downloader: torrent %s no longer active, cleaning up transfer %s", transfer.InfoHash, transfer.ID)
				td.mu.Lock()
				delete(td.active, transfer.ID)
				td.mu.Unlock()
				return
			}

			// Report progress to main server
			td.reportProgressToMain(transfer.ID, stats)

			// Check if completed
			if stats.Progress >= 100 {
				log.Printf("Transfer downloader: download complete for %q (transfer=%s)", transfer.PackageName, transfer.ID)

				// Mark transfer as completed on main server
				td.updateTransferOnMain(transfer.ID, "completed", "")

				// Sync package metadata from main and register in local inventory
				td.syncPackageToLocal(transfer)

				// Register as seeder on main server
				td.registerSeederOnMain(transfer)

				// Remove from active map
				td.mu.Lock()
				delete(td.active, transfer.ID)
				td.mu.Unlock()

				return
			}
		}
	}
}

// reportProgressToMain sends download progress for a specific transfer to the main server.
func (td *TransferDownloader) reportProgressToMain(transferID string, stats *TorrentStats) {
	url := fmt.Sprintf("%s/api/v1/transfers/%s", td.mainServerURL, transferID)

	update := map[string]interface{}{
		"status":           "downloading",
		"progress_percent": stats.Progress,
		"downloaded_bytes": stats.BytesCompleted,
		"download_speed_bps": stats.DownloadSpeed,
		"upload_speed_bps":   stats.UploadSpeed,
		"peers_connected":    stats.PeersConnected,
		"eta_seconds":        stats.ETA,
	}

	data, err := json.Marshal(update)
	if err != nil {
		return
	}

	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := td.httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

// updateTransferOnMain updates a transfer's status on the main server.
func (td *TransferDownloader) updateTransferOnMain(transferID, status, errorMsg string) {
	url := fmt.Sprintf("%s/api/v1/transfers/%s", td.mainServerURL, transferID)

	update := map[string]interface{}{
		"status": status,
	}
	if errorMsg != "" {
		update["error_message"] = errorMsg
	}

	data, err := json.Marshal(update)
	if err != nil {
		log.Printf("Transfer downloader: failed to marshal status update: %v", err)
		return
	}

	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Transfer downloader: failed to create request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := td.httpClient.Do(req)
	if err != nil {
		log.Printf("Transfer downloader: failed to update transfer %s status to %s: %v", transferID, status, err)
		return
	}
	resp.Body.Close()

	log.Printf("Transfer downloader: updated transfer %s status to %s on main server", transferID, status)
}

// syncPackageToLocal fetches package metadata from the main server and inserts it into the local DB,
// then registers the download in local inventory.
func (td *TransferDownloader) syncPackageToLocal(transfer PendingTransfer) {
	// Fetch package info from main server
	url := fmt.Sprintf("%s/api/v1/dcps", td.mainServerURL)
	resp, err := td.httpClient.Get(url)
	if err != nil {
		log.Printf("Transfer downloader: failed to fetch package list from main: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Transfer downloader: main server returned %d for package list", resp.StatusCode)
		return
	}

	var result struct {
		DCPs []struct {
			ID             string `json:"id"`
			AssetMapUUID   string `json:"assetmap_uuid"`
			PackageName    string `json:"package_name"`
			ContentTitle   string `json:"content_title"`
			ContentKind    string `json:"content_kind"`
			Issuer         string `json:"issuer"`
			TotalSizeBytes int64  `json:"total_size_bytes"`
			FileCount      int    `json:"file_count"`
		} `json:"dcps"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("Transfer downloader: failed to decode package list: %v", err)
		return
	}

	// Find the package matching our transfer
	for _, pkg := range result.DCPs {
		if pkg.ID != transfer.PackageID {
			continue
		}

		now := time.Now()

		// Upsert package into local database
		packageUUID, err := uuid.Parse(pkg.ID)
		if err != nil {
			log.Printf("Transfer downloader: invalid package ID %s: %v", pkg.ID, err)
			return
		}

		assetMapUUID, err := uuid.Parse(pkg.AssetMapUUID)
		if err != nil {
			log.Printf("Transfer downloader: invalid assetmap UUID %s: %v", pkg.AssetMapUUID, err)
			return
		}

		insertPkg := `
			INSERT INTO dcp_packages (id, assetmap_uuid, package_name, content_title, content_kind, issuer,
			                          total_size_bytes, file_count, discovered_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (assetmap_uuid) DO UPDATE SET
				package_name = EXCLUDED.package_name,
				content_title = EXCLUDED.content_title,
				content_kind = EXCLUDED.content_kind,
				total_size_bytes = EXCLUDED.total_size_bytes,
				file_count = EXCLUDED.file_count,
				updated_at = CURRENT_TIMESTAMP
		`
		_, err = td.db.Exec(insertPkg, packageUUID, assetMapUUID, pkg.PackageName, pkg.ContentTitle,
			pkg.ContentKind, pkg.Issuer, pkg.TotalSizeBytes, pkg.FileCount, now, now, now)
		if err != nil {
			log.Printf("Transfer downloader: failed to insert package %s locally: %v", pkg.PackageName, err)
			return
		}

		// Determine actual download path (downloadPath/torrent_name)
		// The torrent's info.Name is typically the package directory name
		localPath := filepath.Join(td.downloadPath, pkg.PackageName)

		// Register in local inventory
		serverUUID, _ := uuid.Parse(td.serverID)
		invID := uuid.New()
		insertInv := `
			INSERT INTO server_dcp_inventory (id, server_id, package_id, local_path, status, last_verified, created_at, updated_at)
			VALUES ($1, $2, $3, $4, 'online', $5, $6, $7)
			ON CONFLICT (server_id, package_id) DO UPDATE SET
				local_path = EXCLUDED.local_path,
				status = 'online',
				last_verified = EXCLUDED.last_verified,
				updated_at = CURRENT_TIMESTAMP
		`
		_, err = td.db.Exec(insertInv, invID, serverUUID, packageUUID, localPath, now, now, now)
		if err != nil {
			log.Printf("Transfer downloader: failed to register inventory for %s: %v", pkg.PackageName, err)
			return
		}

		log.Printf("Transfer downloader: synced package %q to local DB and registered in inventory", pkg.PackageName)

		// Also save the torrent to local database so ensure_seeding can restore after restart
		td.syncTorrentToLocal(transfer)

		return
	}

	log.Printf("Transfer downloader: package %s not found in main server's DCP list", transfer.PackageID)
}

// syncTorrentToLocal downloads the torrent record and saves it locally.
func (td *TransferDownloader) syncTorrentToLocal(transfer PendingTransfer) {
	// Fetch torrent metadata from main server
	url := fmt.Sprintf("%s/api/v1/torrents/%s", td.mainServerURL, transfer.InfoHash)
	resp, err := td.httpClient.Get(url)
	if err != nil {
		log.Printf("Transfer downloader: failed to fetch torrent info from main: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var torrentInfo struct {
		ID             string `json:"id"`
		PackageID      string `json:"package_id"`
		InfoHash       string `json:"info_hash"`
		PieceSize      int    `json:"piece_size"`
		TotalPieces    int    `json:"total_pieces"`
		FileCount      int    `json:"file_count"`
		TotalSizeBytes int64  `json:"total_size_bytes"`
		CreatedByServerID string `json:"created_by_server_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&torrentInfo); err != nil {
		log.Printf("Transfer downloader: failed to decode torrent info: %v", err)
		return
	}

	// Fetch the actual torrent file
	torrentBytes, err := td.fetchTorrentFile(transfer.InfoHash)
	if err != nil {
		log.Printf("Transfer downloader: failed to fetch torrent file for local storage: %v", err)
		return
	}

	// Save to local dcp_torrents
	insertTorrent := `
		INSERT INTO dcp_torrents (id, package_id, info_hash, torrent_file, piece_size, total_pieces,
		                          created_by_server_id, file_count, total_size_bytes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (info_hash) DO NOTHING
	`

	torrentID := uuid.New().String()
	_, err = td.db.Exec(insertTorrent, torrentID, torrentInfo.PackageID, torrentInfo.InfoHash,
		torrentBytes, torrentInfo.PieceSize, torrentInfo.TotalPieces,
		torrentInfo.CreatedByServerID, torrentInfo.FileCount, torrentInfo.TotalSizeBytes, time.Now())
	if err != nil {
		log.Printf("Transfer downloader: failed to save torrent locally: %v", err)
		return
	}

	log.Printf("Transfer downloader: saved torrent %s to local database", transfer.InfoHash)
}

// registerSeederOnMain tells the main server that this client is now seeding.
func (td *TransferDownloader) registerSeederOnMain(transfer PendingTransfer) {
	url := fmt.Sprintf("%s/api/v1/torrents/%s/seeders", td.mainServerURL, transfer.InfoHash)

	body := map[string]string{
		"server_id":  td.serverID,
		"local_path": filepath.Join(td.downloadPath, transfer.PackageName),
	}

	data, err := json.Marshal(body)
	if err != nil {
		return
	}

	resp, err := td.httpClient.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Transfer downloader: failed to register as seeder on main: %v", err)
		return
	}
	resp.Body.Close()

	log.Printf("Transfer downloader: registered as seeder for %s on main server", transfer.PackageName)
}
