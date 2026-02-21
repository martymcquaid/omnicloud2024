package torrent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TransferProcessor polls the main server for pending transfers and initiates downloads
type TransferProcessor struct {
	client        *Client
	mainServerURL string
	serverID      string
	macAddress    string
	downloadDir   string // Base directory for downloads (cfg.ScanPath)
	pollInterval  time.Duration
	stopChan      chan struct{}
}

// PendingTransfer represents a transfer waiting to be executed
type PendingTransfer struct {
	ID                  string `json:"id"`
	TorrentID           string `json:"torrent_id"`
	PackageID           string `json:"package_id"`
	PackageName         string `json:"package_name"`
	SourceServerID      string `json:"source_server_id"`
	DestinationServerID string `json:"destination_server_id"`
	Priority            int    `json:"priority"`
	TotalSizeBytes      int64  `json:"total_size_bytes"`
	TorrentFileURL      string `json:"torrent_file_url"`
}

// NewTransferProcessor creates a new transfer processor
func NewTransferProcessor(client *Client, mainServerURL, serverID, macAddress, downloadDir string) *TransferProcessor {
	return &TransferProcessor{
		client:        client,
		mainServerURL: mainServerURL,
		serverID:      serverID,
		macAddress:    macAddress,
		downloadDir:   downloadDir,
		pollInterval:  30 * time.Second,
		stopChan:      make(chan struct{}),
	}
}

// Start begins polling for pending transfers
func (tp *TransferProcessor) Start(ctx context.Context) {
	log.Printf("[transfer-processor] Started - polling %s every %s for server %s (download dir: %s)",
		tp.mainServerURL, tp.pollInterval, tp.serverID, tp.downloadDir)

	// Initial check
	tp.checkPendingTransfers()
	tp.checkCancelledTransfers()
	tp.checkContentCommands()

	ticker := time.NewTicker(tp.pollInterval)
	defer ticker.Stop()

	// Check for commands more frequently (every 10s)
	cancelTicker := time.NewTicker(10 * time.Second)
	defer cancelTicker.Stop()

	for {
		select {
		case <-ticker.C:
			tp.checkPendingTransfers()
		case <-cancelTicker.C:
			tp.checkCancelledTransfers()
			tp.checkContentCommands()
		case <-tp.stopChan:
			log.Println("[transfer-processor] Stopped")
			return
		case <-ctx.Done():
			log.Println("[transfer-processor] Context cancelled, stopping")
			return
		}
	}
}

// Stop stops the transfer processor
func (tp *TransferProcessor) Stop() {
	close(tp.stopChan)
}

// ReportTransferError reports a transfer error to the main server via the API.
// This is used by the torrent client's error reporter callback.
func (tp *TransferProcessor) ReportTransferError(transferID, status, errorMessage string) error {
	return tp.updateTransferStatusWithError(transferID, status, errorMessage)
}

// checkPendingTransfers polls the main server for queued transfers
func (tp *TransferProcessor) checkPendingTransfers() {
	url := fmt.Sprintf("%s/api/v1/servers/%s/pending-transfers", tp.mainServerURL, tp.serverID)

	log.Printf("[transfer-processor] Polling for pending transfers: %s", url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("[transfer-processor] ERROR creating request: %v", err)
		return
	}

	req.Header.Set("X-Server-ID", tp.serverID)
	req.Header.Set("X-MAC-Address", tp.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[transfer-processor] ERROR fetching pending transfers: %v", err)
		return
	}
	defer resp.Body.Close()

	// Read the full body for logging
	body, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusForbidden {
		log.Printf("[transfer-processor] FORBIDDEN (403) - server may not be authorized. Response: %s", string(body))
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[transfer-processor] Unexpected status %d. Response: %s", resp.StatusCode, string(body))
		return
	}

	var transfers []PendingTransfer
	if err := json.Unmarshal(body, &transfers); err != nil {
		log.Printf("[transfer-processor] ERROR decoding response: %v (body: %s)", err, string(body))
		return
	}

	log.Printf("[transfer-processor] Poll result: %d pending transfer(s) found", len(transfers))

	if len(transfers) == 0 {
		return
	}

	// Log each transfer found
	for i, t := range transfers {
		log.Printf("[transfer-processor]   [%d] Transfer %s: package=%q torrent=%s size=%d bytes url=%s",
			i+1, t.ID, t.PackageName, t.TorrentID, t.TotalSizeBytes, t.TorrentFileURL)
	}

	// Check which are already being downloaded by this client
	tp.client.mu.RLock()
	activeCount := len(tp.client.torrents)
	activeTorrents := make(map[string]bool)
	for hash := range tp.client.torrents {
		activeTorrents[hash] = true
	}
	tp.client.mu.RUnlock()
	log.Printf("[transfer-processor] Currently active torrents in client: %d", activeCount)

	// Process each transfer (skip already-active ones)
	for _, transfer := range transfers {
		// Extract info_hash from the torrent_file_url (format: /api/v1/torrents/<info_hash>/file)
		// to check if we're already downloading it
		urlParts := strings.Split(transfer.TorrentFileURL, "/")
		var infoHash string
		for i, p := range urlParts {
			if p == "torrents" && i+1 < len(urlParts) {
				infoHash = urlParts[i+1]
				break
			}
		}

		if infoHash != "" && activeTorrents[infoHash] {
			log.Printf("[transfer-processor] Transfer %s (%s) already active (hash=%s), skipping",
				transfer.ID, transfer.PackageName, infoHash[:12])
			continue
		}

		log.Printf("[transfer-processor] Processing transfer %s (%s)...", transfer.ID, transfer.PackageName)
		if err := tp.initiateTransfer(transfer); err != nil {
			log.Printf("[transfer-processor] ERROR initiating transfer %s: %v", transfer.ID, err)
		}
	}
}

// initiateTransfer downloads the torrent file and starts the download
func (tp *TransferProcessor) initiateTransfer(transfer PendingTransfer) error {
	log.Printf("[transfer-processor] Initiating transfer %s: package=%q size=%d bytes, torrent=%s",
		transfer.ID, transfer.PackageName, transfer.TotalSizeBytes, transfer.TorrentID)

	// Update transfer status to 'downloading'
	log.Printf("[transfer-processor] Setting transfer %s status to 'downloading'...", transfer.ID)
	if err := tp.updateTransferStatus(transfer.ID, "downloading"); err != nil {
		return fmt.Errorf("failed to update transfer status to downloading: %w", err)
	}

	// Download torrent file from main server using the torrent_file_url
	torrentURL := tp.mainServerURL + transfer.TorrentFileURL
	log.Printf("[transfer-processor] Downloading torrent file from: %s", torrentURL)
	torrentBytes, err := tp.downloadTorrentFile(torrentURL)
	if err != nil {
		log.Printf("[transfer-processor] FAILED to download torrent file for %s: %v", transfer.ID, err)
		tp.updateTransferStatus(transfer.ID, "failed")
		return fmt.Errorf("failed to download torrent file: %w", err)
	}

	log.Printf("[transfer-processor] Downloaded torrent file: %d bytes for package %q", len(torrentBytes), transfer.PackageName)

	// Use configured download directory (DCP library scan path)
	destPath := filepath.Join(tp.downloadDir, transfer.PackageName)
	log.Printf("[transfer-processor] Download destination: %s", destPath)

	// Start download via torrent client
	log.Printf("[transfer-processor] Calling StartDownload for transfer %s...", transfer.ID)
	if err := tp.client.StartDownload(torrentBytes, destPath, transfer.PackageID, transfer.ID); err != nil {
		log.Printf("[transfer-processor] FAILED StartDownload for %s: %v", transfer.ID, err)
		tp.updateTransferStatus(transfer.ID, "failed")
		return fmt.Errorf("failed to start download: %w", err)
	}

	log.Printf("[transfer-processor] SUCCESS: Transfer %s started -> %s", transfer.ID, destPath)
	return nil
}

// downloadTorrentFile downloads a torrent file from the main server
func (tp *TransferProcessor) downloadTorrentFile(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("X-Server-ID", tp.serverID)
	req.Header.Set("X-MAC-Address", tp.macAddress)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// updateTransferStatus updates the transfer status on the main server
func (tp *TransferProcessor) updateTransferStatus(transferID, status string) error {
	return tp.updateTransferStatusWithError(transferID, status, "")
}

// updateTransferStatusWithError updates the transfer status and optionally sets an error message on the main server
func (tp *TransferProcessor) updateTransferStatusWithError(transferID, status, errorMessage string) error {
	url := fmt.Sprintf("%s/api/v1/transfers/%s", tp.mainServerURL, transferID)

	payload := map[string]interface{}{
		"status": status,
	}
	if errorMessage != "" {
		payload["error_message"] = errorMessage
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", tp.serverID)
	req.Header.Set("X-MAC-Address", tp.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	if errorMessage != "" {
		log.Printf("[transfer-processor] Updated transfer %s status to %q with error: %s", transferID, status, errorMessage)
	} else {
		log.Printf("[transfer-processor] Updated transfer %s status to %q", transferID, status)
	}
	return nil
}

// TransferCommand represents a command from the main server for a specific transfer
type TransferCommand struct {
	ID          string `json:"id"`
	InfoHash    string `json:"info_hash"`
	PackageName string `json:"package_name"`
	Command     string `json:"command"`    // "pause", "resume", "cancel"
	DeleteData  bool   `json:"delete_data"` // only relevant for cancel
	Status      string `json:"status"`
}

// checkCancelledTransfers polls the main server for pending transfer commands
func (tp *TransferProcessor) checkCancelledTransfers() {
	url := fmt.Sprintf("%s/api/v1/servers/%s/transfer-commands", tp.mainServerURL, tp.serverID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Server-ID", tp.serverID)
	req.Header.Set("X-MAC-Address", tp.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var commands []TransferCommand
	if err := json.NewDecoder(resp.Body).Decode(&commands); err != nil {
		return
	}

	for _, cmd := range commands {
		log.Printf("[transfer-processor] Command received: %s for transfer=%s package=%q info_hash=%s",
			cmd.Command, cmd.ID, cmd.PackageName, cmd.InfoHash)
		tp.executeCommand(cmd)
	}
}

// executeCommand processes a single transfer command
func (tp *TransferProcessor) executeCommand(cmd TransferCommand) {
	var result, message string

	switch cmd.Command {
	case "pause":
		if cmd.InfoHash != "" {
			if err := tp.client.PauseTorrent(cmd.InfoHash); err != nil {
				log.Printf("[transfer-processor] Could not pause %s: %v", cmd.InfoHash[:12], err)
				result = "error"
				message = fmt.Sprintf("Pause failed: %v", err)
			} else {
				log.Printf("[transfer-processor] PAUSED torrent %s (%s)", cmd.InfoHash[:12], cmd.PackageName)
				result = "done"
				message = "Torrent paused"
			}
		} else {
			result = "done"
			message = "No info_hash, nothing to pause"
		}

	case "resume":
		if cmd.InfoHash != "" {
			if err := tp.client.ResumeTorrent(cmd.InfoHash); err != nil {
				log.Printf("[transfer-processor] Could not resume %s: %v", cmd.InfoHash[:12], err)
				result = "error"
				message = fmt.Sprintf("Resume failed: %v", err)
			} else {
				log.Printf("[transfer-processor] RESUMED torrent %s (%s)", cmd.InfoHash[:12], cmd.PackageName)
				result = "done"
				message = "Torrent resumed"
			}
		} else {
			result = "done"
			message = "No info_hash, nothing to resume"
		}

	case "cancel":
		result = "kept"
		message = "Transfer stopped"

		// Stop the torrent if it's active
		if cmd.InfoHash != "" {
			if err := tp.client.StopTorrent(cmd.InfoHash); err != nil {
				log.Printf("[transfer-processor] Could not stop torrent %s: %v (may already be stopped)", cmd.InfoHash[:12], err)
			} else {
				log.Printf("[transfer-processor] Stopped torrent %s (%s)", cmd.InfoHash[:12], cmd.PackageName)
			}
		}

		// Delete DCP data if requested
		if cmd.DeleteData && cmd.PackageName != "" {
			dcpPath := filepath.Join(tp.downloadDir, cmd.PackageName)
			log.Printf("[transfer-processor] Deleting DCP data: %s", dcpPath)

			absPath, err := filepath.Abs(dcpPath)
			if err != nil || !strings.HasPrefix(absPath, tp.downloadDir) {
				log.Printf("[transfer-processor] REFUSING to delete %s — not under download dir %s", dcpPath, tp.downloadDir)
				result = "error"
				message = "Delete refused: path outside download directory"
			} else {
				info, statErr := os.Stat(dcpPath)
				if statErr != nil {
					if os.IsNotExist(statErr) {
						result = "deleted"
						message = "Data already deleted or not found"
					} else {
						result = "error"
						message = fmt.Sprintf("Error checking path: %v", statErr)
					}
				} else if !info.IsDir() {
					result = "error"
					message = "Path is not a directory"
				} else {
					if err := os.RemoveAll(dcpPath); err != nil {
						log.Printf("[transfer-processor] ERROR deleting DCP data %s: %v", dcpPath, err)
						result = "error"
						message = fmt.Sprintf("Delete failed: %v", err)
					} else {
						log.Printf("[transfer-processor] DELETED DCP data: %s", dcpPath)
						result = "deleted"
						message = fmt.Sprintf("Deleted %s", dcpPath)
					}
				}
			}
		}

		// Clean up piece completion data
		if cmd.DeleteData && cmd.InfoHash != "" && tp.client.db != nil {
			_, err := tp.client.db.Exec("DELETE FROM torrent_piece_completion WHERE info_hash = $1", cmd.InfoHash)
			if err != nil {
				log.Printf("[transfer-processor] Error cleaning piece completion for %s: %v", cmd.InfoHash[:12], err)
			} else {
				log.Printf("[transfer-processor] Cleaned piece completion data for %s", cmd.InfoHash[:12])
			}
		}

	default:
		log.Printf("[transfer-processor] Unknown command: %s", cmd.Command)
		result = "error"
		message = fmt.Sprintf("Unknown command: %s", cmd.Command)
	}

	// Acknowledge the command
	tp.acknowledgeCommand(cmd.ID, result, message)
}

// acknowledgeCommand tells the main server we've processed the command
func (tp *TransferProcessor) acknowledgeCommand(transferID, result, message string) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/transfer-command-ack", tp.mainServerURL, tp.serverID)

	payload := map[string]string{
		"transfer_id": transferID,
		"result":      result,
		"message":     message,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", tp.serverID)
	req.Header.Set("X-MAC-Address", tp.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[transfer-processor] Error sending command-ack for %s: %v", transferID, err)
		return
	}
	defer resp.Body.Close()

	log.Printf("[transfer-processor] Command acknowledged for transfer %s: %s (%s)", transferID, result, message)
}

// DeleteContent performs a DCP deletion. Called both by the polling-based content command system
// and by the WebSocket delete_content command handler.
// Returns (result, message) where result is "deleted" or "error".
func (tp *TransferProcessor) DeleteContent(packageID, packageName, infoHash, targetPath string) (string, string) {
	result := "deleted"
	message := "Content deleted"

	// Determine which path to delete
	var deletePath string
	if targetPath != "" {
		deletePath = targetPath
		log.Printf("[delete-content] Deleting from specific path: %s", deletePath)
	} else if packageName != "" {
		deletePath = filepath.Join(tp.downloadDir, packageName)
	}

	// Stop the torrent if loaded (only if deleting from all locations or download location)
	if infoHash != "" && targetPath == "" {
		if err := tp.client.StopTorrent(infoHash); err != nil {
			log.Printf("[delete-content] Could not stop torrent %s: %v (may not be loaded)", infoHash, err)
		} else {
			log.Printf("[delete-content] Stopped torrent for %s", packageName)
		}
	}

	// Delete DCP data
	if deletePath != "" {
		absPath, err := filepath.Abs(deletePath)
		if err != nil {
			result = "error"
			message = fmt.Sprintf("Invalid path: %v", err)
		} else {
			info, statErr := os.Stat(absPath)
			if statErr != nil {
				if os.IsNotExist(statErr) {
					result = "deleted"
					message = "Data already deleted or not found"
				} else {
					result = "error"
					message = fmt.Sprintf("Error checking path: %v", statErr)
				}
			} else if !info.IsDir() {
				result = "error"
				message = "Path is not a directory"
			} else {
				if err := os.RemoveAll(absPath); err != nil {
					log.Printf("[delete-content] ERROR deleting %s: %v", absPath, err)
					result = "error"
					message = fmt.Sprintf("Delete failed: %v", err)
				} else {
					log.Printf("[delete-content] DELETED: %s", absPath)
					result = "deleted"
					message = fmt.Sprintf("Deleted %s", absPath)
				}
			}
		}
	} else {
		result = "error"
		message = "No package name or target path provided"
	}

	// Clean up piece completion data (only when not targeting a specific location)
	if infoHash != "" && tp.client.db != nil && targetPath == "" {
		tp.client.db.Exec("DELETE FROM torrent_piece_completion WHERE info_hash = $1", infoHash)
	}

	// Remove from local inventory (only when not targeting a specific location)
	if packageID != "" && tp.client.db != nil && targetPath == "" {
		tp.client.db.Exec("DELETE FROM server_dcp_inventory WHERE server_id = $1 AND package_id = $2", tp.serverID, packageID)
	}

	return result, message
}

// ContentCommand represents a content management command from the main server
type ContentCommand struct {
	ID          string `json:"id"`
	PackageID   string `json:"package_id"`
	PackageName string `json:"package_name"`
	InfoHash    string `json:"info_hash"`
	Command     string `json:"command"`     // "delete"
	TargetPath  string `json:"target_path"` // optional: specific path to delete from (e.g., RosettaBridge location)
}

// checkContentCommands polls the main server for pending content commands (e.g., delete)
func (tp *TransferProcessor) checkContentCommands() {
	url := fmt.Sprintf("%s/api/v1/servers/%s/content-commands", tp.mainServerURL, tp.serverID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Header.Set("X-Server-ID", tp.serverID)
	req.Header.Set("X-MAC-Address", tp.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var commands []ContentCommand
	if err := json.NewDecoder(resp.Body).Decode(&commands); err != nil {
		return
	}

	for _, cmd := range commands {
		log.Printf("[content-cmd] Received: %s for package=%q info_hash=%s", cmd.Command, cmd.PackageName, cmd.InfoHash)
		tp.executeContentCommand(cmd)
	}
}

// executeContentCommand processes a content management command
func (tp *TransferProcessor) executeContentCommand(cmd ContentCommand) {
	var result, message string

	switch cmd.Command {
	case "delete":
		result, message = tp.DeleteContent(cmd.PackageID, cmd.PackageName, cmd.InfoHash, cmd.TargetPath)
	default:
		result = "error"
		message = fmt.Sprintf("Unknown command: %s", cmd.Command)
	}

	// Acknowledge
	tp.acknowledgeContentCommand(cmd.ID, result, message)
}

// acknowledgeContentCommand tells the main server we've processed the content command
func (tp *TransferProcessor) acknowledgeContentCommand(commandID, result, message string) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/content-command-ack", tp.mainServerURL, tp.serverID)

	payload := map[string]string{
		"command_id": commandID,
		"result":     result,
		"message":    message,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", tp.serverID)
	req.Header.Set("X-MAC-Address", tp.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[content-cmd] Error sending ack for %s: %v", commandID, err)
		return
	}
	defer resp.Body.Close()

	log.Printf("[content-cmd] Acknowledged command %s: %s (%s)", commandID, result, message)
}
