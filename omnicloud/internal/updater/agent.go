package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/omnicloud/omnicloud/internal/db"
)

// #region agent log
const debugLogPath = "/home/appbox/DCPCLOUDAPP/.cursor/debug.log"
var debugLogMu sync.Mutex
func agentDebugLog(hypothesisId, location, message string, data map[string]interface{}) {
	debugLogMu.Lock()
	defer debugLogMu.Unlock()
	payload := map[string]interface{}{"hypothesisId": hypothesisId, "location": location, "message": message, "data": data, "timestamp": time.Now().UnixNano() / 1e6}
	if f, err := os.OpenFile(debugLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); err == nil {
		enc := json.NewEncoder(f)
		enc.SetEscapeHTML(false)
		enc.Encode(payload)
		f.Close()
	}
	// Also emit to process log so remote server's omnicloud.log shows agent activity
	dataStr := ""
	if len(data) > 0 {
		b, _ := json.Marshal(data)
		dataStr = string(b)
	}
	log.Printf("[agent-debug] %s %s %s %s", hypothesisId, location, message, dataStr)
}
// #endregion

// Agent handles automatic software updates
type Agent struct {
	database       *db.DB
	serverID       uuid.UUID
	mainServerURL  string
	macAddress     string
	currentVersion string
	checkInterval  time.Duration
	stopChan       chan struct{}
}

// PendingAction is the response from the main server's pending-action endpoint
type PendingAction struct {
	Action        *string `json:"action"`         // "restart", "upgrade", or null
	TargetVersion string  `json:"target_version"` // set when action is "upgrade"
}

// VersionInfo represents version metadata from main server
type VersionInfo struct {
	Version     string `json:"version"`
	BuildTime   string `json:"build_time"`
	Checksum    string `json:"checksum"`
	SizeBytes   int64  `json:"size_bytes"`
	DownloadURL string `json:"download_url"`
}

// NewAgent creates a new update agent
func NewAgent(database *db.DB, serverID uuid.UUID, mainServerURL, macAddress, currentVersion string) *Agent {
	return &Agent{
		database:       database,
		serverID:       serverID,
		mainServerURL:  mainServerURL,
		macAddress:     macAddress,
		currentVersion: currentVersion,
		checkInterval:  60 * time.Second, // check every minute so restarts are picked up quickly
		stopChan:       make(chan struct{}),
	}
}

// Start begins the update checking loop
func (a *Agent) Start() {
	// #region agent log
	agentDebugLog("H1", "updater/agent.go:Start", "Update agent Start() entered", map[string]interface{}{
		"mainServerURL": a.mainServerURL, "serverID": a.serverID.String(), "macAddress": a.macAddress, "checkIntervalSec": int(a.checkInterval.Seconds()),
	})
	// #endregion
	log.Println("Update agent started - checking main server for pending actions every minute")

	ticker := time.NewTicker(a.checkInterval)
	defer ticker.Stop()

	// Check immediately on startup
	go a.checkForUpdates()

	for {
		select {
		case <-ticker.C:
			a.checkForUpdates()
		case <-a.stopChan:
			log.Println("Update agent stopped")
			return
		}
	}
}

// Stop stops the update agent
func (a *Agent) Stop() {
	close(a.stopChan)
}

// checkForUpdates asks the main server for any pending restart/upgrade action
func (a *Agent) checkForUpdates() {
	// #region agent log
	agentDebugLog("H2", "updater/agent.go:checkForUpdates", "checkForUpdates running", nil)
	// #endregion
	action, targetVersion, err := a.fetchPendingAction()
	if err != nil {
		log.Printf("Update agent: error fetching pending action: %v", err)
		// #region agent log
		agentDebugLog("H3", "updater/agent.go:checkForUpdates", "fetchPendingAction error", map[string]interface{}{"error": err.Error()})
		// #endregion
		return
	}
	// #region agent log
	actionStr := ""
	if action != nil {
		actionStr = *action
	}
	agentDebugLog("H5", "updater/agent.go:checkForUpdates", "fetchPendingAction result", map[string]interface{}{"action": actionStr, "targetVersion": targetVersion})
	// #endregion
	if action == nil {
		return
	}

	switch *action {
	case "restart":
		log.Println("🔄 Restart requested by administrator")
		a.handleRestart()
	case "upgrade":
		if targetVersion != "" && targetVersion != a.currentVersion {
			log.Printf("⬆️  Upgrade available: %s -> %s", a.currentVersion, targetVersion)
			a.performUpgrade(targetVersion)
		}
	}
}

// fetchPendingAction calls the main server (with auth) and returns action + target_version
func (a *Agent) fetchPendingAction() (action *string, targetVersion string, err error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/pending-action", a.mainServerURL, a.serverID.String())
	// #region agent log
	agentDebugLog("H2", "updater/agent.go:fetchPendingAction", "request starting", map[string]interface{}{"url": url})
	// #endregion
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("X-Server-ID", a.serverID.String())
	req.Header.Set("X-MAC-Address", a.macAddress)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	bodyBytes, readErr := ioutil.ReadAll(resp.Body)
	// #region agent log
	bodySnap := string(bodyBytes)
	if len(bodySnap) > 500 {
		bodySnap = bodySnap[:500]
	}
	agentDebugLog("H3", "updater/agent.go:fetchPendingAction", "response received", map[string]interface{}{
		"statusCode": resp.StatusCode, "body": bodySnap,
	})
	if resp.StatusCode == http.StatusForbidden {
		agentDebugLog("H4", "updater/agent.go:fetchPendingAction", "403 Forbidden", nil)
	}
	// #endregion
	if resp.StatusCode == http.StatusForbidden {
		return nil, "", nil // not authorized, skip
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("pending-action returned %d", resp.StatusCode)
	}
	if readErr != nil {
		return nil, "", readErr
	}

	var pa PendingAction
	if err := json.Unmarshal(bodyBytes, &pa); err != nil {
		return nil, "", err
	}
	return pa.Action, pa.TargetVersion, nil
}

// notifyActionDone tells the main server that we completed the action (so it clears the flag)
func (a *Agent) notifyActionDone(action, status string) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/action-done", a.mainServerURL, a.serverID.String())
	body := []byte(fmt.Sprintf(`{"action":%q,"status":%q}`, action, status))
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		log.Printf("Update agent: failed to create action-done request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", a.serverID.String())
	req.Header.Set("X-MAC-Address", a.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Update agent: failed to notify action-done: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("Update agent: action-done returned %d", resp.StatusCode)
		return
	}
	log.Printf("Update agent: notified main server action %s %s", action, status)
}

// performUpgrade downloads and installs a new version
func (a *Agent) performUpgrade(targetVersion string) {
	log.Printf("Starting upgrade to version %s...", targetVersion)

	// Get version info from main server
	versionInfo, err := a.getVersionInfo(targetVersion)
	if err != nil {
		log.Printf("❌ Failed to get version info: %v", err)
		a.notifyActionDone("upgrade", "failed")
		return
	}

	// Download package
	packagePath := fmt.Sprintf("/tmp/omnicloud-%s.tar.gz", targetVersion)
	if err := a.downloadPackage(versionInfo.DownloadURL, packagePath); err != nil {
		log.Printf("❌ Failed to download package: %v", err)
		a.notifyActionDone("upgrade", "failed")
		return
	}

	// Verify checksum
	if err := a.verifyChecksum(packagePath, versionInfo.Checksum); err != nil {
		log.Printf("❌ Checksum verification failed: %v", err)
		os.Remove(packagePath)
		a.notifyActionDone("upgrade", "failed")
		return
	}

	log.Println("✓ Package downloaded and verified")

	// Extract to staging directory
	stagingDir := fmt.Sprintf("/tmp/omnicloud-upgrade-%s", targetVersion)
	os.RemoveAll(stagingDir)
	if err := a.extractPackage(packagePath, stagingDir); err != nil {
		log.Printf("❌ Failed to extract package: %v", err)
		os.Remove(packagePath)
		a.notifyActionDone("upgrade", "failed")
		return
	}

	log.Println("✓ Package extracted")

	// Create backup of current binary
	currentBinary := "/opt/omnicloud/bin/omnicloud"
	backupBinary := "/opt/omnicloud/bin/omnicloud.backup"
	if _, err := os.Stat(currentBinary); err == nil {
		if err := copyFile(currentBinary, backupBinary); err != nil {
			log.Printf("⚠️  Warning: failed to create backup: %v", err)
		}
	}

	// Replace binary
	newBinary := filepath.Join(stagingDir, fmt.Sprintf("omnicloud-%s-linux-amd64", targetVersion), "omnicloud")
	if err := copyFile(newBinary, currentBinary); err != nil {
		log.Printf("❌ Failed to replace binary: %v", err)
		a.notifyActionDone("upgrade", "failed")
		return
	}

	os.Chmod(currentBinary, 0755)
	log.Println("✓ Binary updated")

	// Cleanup
	os.Remove(packagePath)
	os.RemoveAll(stagingDir)

	log.Printf("✅ Upgrade to %s complete! Notifying main server and restarting service...", targetVersion)
	a.notifyActionDone("upgrade", "success")

	// Restart the service
	a.restartService()
}

// handleRestart performs a service restart
func (a *Agent) handleRestart() {
	log.Println("Restarting service...")
	// Notify main server so it clears the pending flag (we are about to die)
	a.notifyActionDone("restart", "success")
	a.restartService()
}

// restartService restarts the OmniCloud systemd service
func (a *Agent) restartService() {
	// Give a moment for database connection to close
	time.Sleep(1 * time.Second)

	cmd := exec.Command("systemctl", "restart", "omnicloud")
	if err := cmd.Run(); err != nil {
		log.Printf("❌ Failed to restart service: %v", err)
		log.Println("Please manually restart: systemctl restart omnicloud")
	}
}

// getVersionInfo fetches version metadata from main server
func (a *Agent) getVersionInfo(version string) (*VersionInfo, error) {
	url := fmt.Sprintf("%s/api/v1/versions", a.mainServerURL)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch versions: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Versions []VersionInfo `json:"versions"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Find the target version
	for _, v := range result.Versions {
		if v.Version == version {
			return &v, nil
		}
	}

	return nil, fmt.Errorf("version %s not found", version)
}

// downloadPackage downloads a release package
func (a *Agent) downloadPackage(downloadURL, destPath string) error {
	// Convert relative URL to absolute
	url := a.mainServerURL + downloadURL

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// verifyChecksumFile verifies the SHA256 checksum of a file (package-level for use by self-upgrade).
func verifyChecksumFile(filePath, expectedChecksum string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return fmt.Errorf("failed to compute hash: %w", err)
	}

	actualChecksum := hex.EncodeToString(hash.Sum(nil))
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedChecksum, actualChecksum)
	}

	return nil
}

func (a *Agent) verifyChecksum(filePath, expectedChecksum string) error {
	return verifyChecksumFile(filePath, expectedChecksum)
}

// extractPackageFile extracts a tar.gz file (package-level for use by self-upgrade).
func extractPackageFile(tarPath, destDir string) error {
	os.MkdirAll(destDir, 0755)

	file, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("failed to open tar file: %w", err)
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		target := filepath.Join(destDir, header.Name)

		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("failed to write file: %w", err)
			}
			f.Close()
		}
	}

	return nil
}

func (a *Agent) extractPackage(tarPath, destDir string) error {
	return extractPackageFile(tarPath, destDir)
}

// updateUpgradeStatus updates the upgrade status in the database
func (a *Agent) updateUpgradeStatus(status string) {
	_, err := a.database.DB.Exec(`UPDATE servers SET upgrade_status = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, status, a.serverID)
	if err != nil {
		log.Printf("Failed to update upgrade status: %v", err)
	}
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}
