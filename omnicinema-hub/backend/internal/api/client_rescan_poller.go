package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/db"
)

// RescanPoller polls the main server for rescan commands and runs the local full scan when requested.
type RescanPoller struct {
	database      *db.DB
	serverID      uuid.UUID
	mainServerURL string
	runFullScan   func()
	stopChan      chan struct{}
	interval      time.Duration
}

// NewRescanPoller creates a poller that checks for rescan and invokes runFullScan when needed.
func NewRescanPoller(database *db.DB, serverID uuid.UUID, mainServerURL string, runFullScan func()) *RescanPoller {
	return &RescanPoller{
		database:      database,
		serverID:      serverID,
		mainServerURL: mainServerURL,
		runFullScan:   runFullScan,
		stopChan:      make(chan struct{}),
		interval:      60 * time.Second,
	}
}

// Start begins polling for pending rescan in a goroutine; when the main server has set rescan, runs full scan and reports result.
func (p *RescanPoller) Start() {
	log.Println("Rescan poller started - checking main server for rescan command every 60s")
	go p.loop()
}

func (p *RescanPoller) loop() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.checkPendingRescan()
		case <-p.stopChan:
			log.Println("Rescan poller stopped")
			return
		}
	}
}

// Stop stops the poller.
func (p *RescanPoller) Stop() {
	close(p.stopChan)
}

func (p *RescanPoller) checkPendingRescan() {
	url := fmt.Sprintf("%s/api/v1/servers/%s/pending-action", p.mainServerURL, p.serverID.String())
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("Rescan poller: failed to create request: %v", err)
		return
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Rescan poller: failed to fetch pending action: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var out struct {
		Action *string `json:"action"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return
	}
	if out.Action == nil || *out.Action != "rescan" {
		return
	}

	log.Println("Rescan requested by main server - running full library scan now")
	p.runFullScan()

	// Get latest scan log to report to main server
	scanLog, err := p.database.GetLatestScanLog(p.serverID)
	status := "success"
	message := ""
	scanStatus := map[string]interface{}{"status": status}
	if err != nil {
		status = "error"
		message = err.Error()
		scanStatus["status"] = status
		scanStatus["message"] = message
	} else if scanLog != nil {
		scanStatus["status"] = scanLog.Status
		scanStatus["server_id"] = p.serverID.String()
		scanStatus["scan_type"] = scanLog.ScanType
		scanStatus["started_at"] = scanLog.StartedAt
		scanStatus["completed_at"] = scanLog.CompletedAt
		scanStatus["packages_found"] = scanLog.PackagesFound
		scanStatus["packages_added"] = scanLog.PackagesAdded
		scanStatus["packages_updated"] = scanLog.PackagesUpdated
		scanStatus["packages_removed"] = scanLog.PackagesRemoved
		scanStatus["errors"] = scanLog.Errors
		if scanLog.Errors != "" {
			scanStatus["message"] = scanLog.Errors
		}
	}

	body := map[string]interface{}{
		"action":      "rescan",
		"status":     status,
		"message":    message,
		"scan_status": scanStatus,
	}
	bodyBytes, _ := json.Marshal(body)
	actionURL := fmt.Sprintf("%s/api/v1/servers/%s/action-done", p.mainServerURL, p.serverID.String())
	req2, _ := http.NewRequest("POST", actionURL, bytes.NewReader(bodyBytes))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := client.Do(req2)
	if err != nil {
		log.Printf("Rescan poller: failed to notify action-done: %v", err)
		return
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		log.Println("Rescan complete - main server notified")
	} else {
		log.Printf("Rescan poller: action-done returned %d", resp2.StatusCode)
	}
}
