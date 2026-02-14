package torrent

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/bencode"
	"github.com/google/uuid"
)

// TorrentDownloader handles downloading existing torrents from main server
type TorrentDownloader struct {
	db            *sql.DB
	mainServerURL string
	serverID      string
	macAddress    string
}

// NewTorrentDownloader creates a new torrent downloader
func NewTorrentDownloader(db *sql.DB, mainServerURL, serverID, macAddress string) *TorrentDownloader {
	return &TorrentDownloader{
		db:            db,
		mainServerURL: mainServerURL,
		serverID:      serverID,
		macAddress:    macAddress,
	}
}

// TryDownloadExistingTorrent attempts to download a torrent from main server
// Returns true if torrent was found and downloaded successfully
func (td *TorrentDownloader) TryDownloadExistingTorrent(packageID uuid.UUID, packagePath string) (bool, error) {
	// Query main server for torrent by package ID
	url := fmt.Sprintf("%s/api/v1/torrents?package_id=%s", td.mainServerURL, packageID)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Server-ID", td.serverID)
	req.Header.Set("X-MAC-Address", td.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to query torrents: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("main server returned status %d", resp.StatusCode)
	}

	var result struct {
		Count    int                      `json:"count"`
		Torrents []map[string]interface{} `json:"torrents"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.Count == 0 {
		// No torrent exists on main server
		return false, nil
	}

	// Get the first (should be only) torrent
	torrentData := result.Torrents[0]
	torrentIDStr, ok := torrentData["id"].(string)
	if !ok {
		return false, fmt.Errorf("invalid torrent ID in response")
	}

	torrentID, err := uuid.Parse(torrentIDStr)
	if err != nil {
		return false, fmt.Errorf("failed to parse torrent ID: %w", err)
	}

	log.Printf("Found existing torrent on main server for package %s, downloading...", packageID)

	// Download the torrent file
	torrentBytes, err := td.downloadTorrentFile(torrentID)
	if err != nil {
		return false, fmt.Errorf("failed to download torrent file: %w", err)
	}

	// Verify torrent against local files
	verified, err := td.verifyTorrentAgainstLocalFiles(torrentBytes, packagePath)
	if err != nil {
		log.Printf("Warning: torrent verification failed: %v, will regenerate", err)
		return false, nil
	}

	if !verified {
		log.Printf("Warning: torrent verification failed for %s, will regenerate", packageID)
		return false, nil
	}

	// Save torrent to local database
	if err := td.saveTorrentToDatabase(torrentID, packageID, torrentBytes, torrentData); err != nil {
		return false, fmt.Errorf("failed to save torrent: %w", err)
	}

	log.Printf("Successfully downloaded and verified existing torrent for package %s", packageID)
	return true, nil
}

// downloadTorrentFile downloads a torrent file from main server
func (td *TorrentDownloader) downloadTorrentFile(torrentID uuid.UUID) ([]byte, error) {
	url := fmt.Sprintf("%s/api/v1/torrents/%s/file", td.mainServerURL, torrentID)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Server-ID", td.serverID)
	req.Header.Set("X-MAC-Address", td.macAddress)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	return ioutil.ReadAll(resp.Body)
}

// verifyTorrentAgainstLocalFiles performs quick verification
// Checks that all files exist and sizes match (no full hash check)
func (td *TorrentDownloader) verifyTorrentAgainstLocalFiles(torrentBytes []byte, packagePath string) (bool, error) {
	var mi metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentBytes, &mi); err != nil {
		return false, fmt.Errorf("failed to parse torrent: %w", err)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		return false, fmt.Errorf("failed to unmarshal info: %w", err)
	}

	// Verify each file
	for _, file := range info.Files {
		// Build full path
		filePath := filepath.Join(packagePath, filepath.Join(file.Path...))
		
		// Check file exists
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			return false, fmt.Errorf("file not found: %s", filePath)
		}

		// Check size matches
		if fileInfo.Size() != file.Length {
			return false, fmt.Errorf("file size mismatch: %s (expected %d, got %d)",
				filePath, file.Length, fileInfo.Size())
		}
	}

	log.Printf("Quick verification passed: all %d files exist with correct sizes", len(info.Files))
	return true, nil
}

// saveTorrentToDatabase saves a downloaded torrent to the local database
func (td *TorrentDownloader) saveTorrentToDatabase(torrentID, packageID uuid.UUID, torrentBytes []byte, torrentData map[string]interface{}) error {
	// Parse metainfo
	var mi metainfo.MetaInfo
	if err := bencode.Unmarshal(torrentBytes, &mi); err != nil {
		return fmt.Errorf("failed to parse torrent: %w", err)
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		return fmt.Errorf("failed to unmarshal info: %w", err)
	}

	infoHash := mi.HashInfoBytes().HexString()

	// Extract values from torrentData
	trackerURL := mi.Announce
	pieceLength := info.PieceLength
	totalPieces := len(info.Pieces) / 20 // Each piece hash is 20 bytes
	var totalSize int64
	for _, file := range info.Files {
		totalSize += file.Length
	}

	// Save to database
	_, err = td.db.Exec(`
		INSERT INTO dcp_torrents (id, package_id, info_hash, tracker_url, piece_length, total_pieces, total_size_bytes, torrent_file, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (package_id) DO UPDATE SET
			info_hash = EXCLUDED.info_hash,
			tracker_url = EXCLUDED.tracker_url,
			piece_length = EXCLUDED.piece_length,
			total_pieces = EXCLUDED.total_pieces,
			total_size_bytes = EXCLUDED.total_size_bytes,
			torrent_file = EXCLUDED.torrent_file,
			updated_at = EXCLUDED.updated_at
	`, torrentID, packageID, infoHash, trackerURL, pieceLength, totalPieces, totalSize, torrentBytes, time.Now(), time.Now())

	if err != nil {
		return fmt.Errorf("failed to save to database: %w", err)
	}

	return nil
}
