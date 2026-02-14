package torrent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

// TransferProcessor polls the main server for pending transfers and initiates downloads
type TransferProcessor struct {
	client        *Client
	mainServerURL string
	serverID      string
	macAddress    string
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
func NewTransferProcessor(client *Client, mainServerURL, serverID, macAddress string) *TransferProcessor {
	return &TransferProcessor{
		client:        client,
		mainServerURL: mainServerURL,
		serverID:      serverID,
		macAddress:    macAddress,
		pollInterval:  30 * time.Second,
		stopChan:      make(chan struct{}),
	}
}

// Start begins polling for pending transfers
func (tp *TransferProcessor) Start(ctx context.Context) {
	log.Println("Transfer processor started - polling for pending transfers")

	// Initial check
	tp.checkPendingTransfers()

	ticker := time.NewTicker(tp.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tp.checkPendingTransfers()
		case <-tp.stopChan:
			log.Println("Transfer processor stopped")
			return
		case <-ctx.Done():
			log.Println("Transfer processor context cancelled")
			return
		}
	}
}

// Stop stops the transfer processor
func (tp *TransferProcessor) Stop() {
	close(tp.stopChan)
}

// checkPendingTransfers polls the main server for queued transfers
func (tp *TransferProcessor) checkPendingTransfers() {
	url := fmt.Sprintf("%s/api/v1/servers/%s/pending-transfers", tp.mainServerURL, tp.serverID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("Error creating pending transfers request: %v", err)
		return
	}

	req.Header.Set("X-Server-ID", tp.serverID)
	req.Header.Set("X-MAC-Address", tp.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error fetching pending transfers: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		log.Printf("Not authorized to fetch transfers (server may not be authorized)")
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed to fetch pending transfers: status %d", resp.StatusCode)
		return
	}

	var transfers []PendingTransfer
	if err := json.NewDecoder(resp.Body).Decode(&transfers); err != nil {
		log.Printf("Error decoding pending transfers: %v", err)
		return
	}

	if len(transfers) > 0 {
		log.Printf("Found %d pending transfer(s)", len(transfers))
	}

	// Process each transfer
	for _, transfer := range transfers {
		if err := tp.initiateTransfer(transfer); err != nil {
			log.Printf("Error initiating transfer %s: %v", transfer.ID, err)
		}
	}
}

// initiateTransfer downloads the torrent file and starts the download
func (tp *TransferProcessor) initiateTransfer(transfer PendingTransfer) error {
	log.Printf("Initiating transfer: %s (Package: %s, Size: %d bytes)",
		transfer.ID, transfer.PackageName, transfer.TotalSizeBytes)

	// Update transfer status to 'downloading'
	if err := tp.updateTransferStatus(transfer.ID, "downloading"); err != nil {
		return fmt.Errorf("failed to update transfer status: %w", err)
	}

	// Download torrent file from main server
	torrentURL := fmt.Sprintf("%s/api/v1/torrents/%s/file", tp.mainServerURL, transfer.TorrentID)
	torrentBytes, err := tp.downloadTorrentFile(torrentURL)
	if err != nil {
		tp.updateTransferStatus(transfer.ID, "failed")
		return fmt.Errorf("failed to download torrent file: %w", err)
	}

	// Determine destination path (use scan path for now)
	// TODO: Make this configurable or use a dedicated downloads directory
	destPath := "/tmp/omnicloud-downloads/" + transfer.PackageName

	// Start download via torrent client
	if err := tp.client.StartDownload(torrentBytes, destPath, transfer.PackageID, transfer.ID); err != nil {
		tp.updateTransferStatus(transfer.ID, "failed")
		return fmt.Errorf("failed to start download: %w", err)
	}

	log.Printf("Transfer %s started successfully", transfer.ID)
	return nil
}

// downloadTorrentFile downloads a torrent file from the main server
func (tp *TransferProcessor) downloadTorrentFile(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("X-Server-ID", tp.serverID)
	req.Header.Set("X-MAC-Address", tp.macAddress)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download torrent file: status %d", resp.StatusCode)
	}

	return ioutil.ReadAll(resp.Body)
}

// updateTransferStatus updates the transfer status on the main server
func (tp *TransferProcessor) updateTransferStatus(transferID, status string) error {
	url := fmt.Sprintf("%s/api/v1/transfers/%s", tp.mainServerURL, transferID)

	payload := map[string]interface{}{
		"status": status,
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
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to update transfer status: status %d", resp.StatusCode)
	}

	return nil
}
