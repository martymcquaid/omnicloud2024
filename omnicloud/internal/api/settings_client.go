package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// LibraryLocation represents a library path returned from the main server
type LibraryLocation struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	IsActive     bool   `json:"is_active"`
	LocationType string `json:"location_type"`
}

// ServerSettingsResponse is the response from /servers/{id}/settings
type ServerSettingsResponse struct {
	ServerID                    string            `json:"server_id"`
	DownloadLocation            string            `json:"download_location"`
	TorrentDownloadLocation     string            `json:"torrent_download_location"`
	WatchFolder                 string            `json:"watch_folder"`
	AutoCleanupAfterIngestion   bool              `json:"auto_cleanup_after_ingestion"`
	LibraryLocations            []LibraryLocation `json:"library_locations"`
}

// SettingsClient fetches server settings from the main server
type SettingsClient struct {
	mainServerURL string
	serverID      string
	macAddress    string
}

// NewSettingsClient creates a new settings client
func NewSettingsClient(mainServerURL, serverID, macAddress string) *SettingsClient {
	return &SettingsClient{
		mainServerURL: mainServerURL,
		serverID:      serverID,
		macAddress:    macAddress,
	}
}

// GetLibraryLocations fetches the library locations from the main server
func (sc *SettingsClient) GetLibraryLocations() ([]string, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/settings", sc.mainServerURL, sc.serverID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Server-ID", sc.serverID)
	req.Header.Set("X-MAC-Address", sc.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var settings ServerSettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract active library paths
	var paths []string
	for _, lib := range settings.LibraryLocations {
		if lib.IsActive {
			paths = append(paths, lib.Path)
			locType := lib.LocationType
			if locType == "" {
				locType = "standard"
			}
			log.Printf("Using library location: %s (%s) [type: %s]", lib.Name, lib.Path, locType)
		} else {
			log.Printf("Skipping inactive library: %s (%s)", lib.Name, lib.Path)
		}
	}

	return paths, nil
}

// GetDownloadLocation fetches the download location from the main server
func (sc *SettingsClient) GetDownloadLocation() (string, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/settings", sc.mainServerURL, sc.serverID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Server-ID", sc.serverID)
	req.Header.Set("X-MAC-Address", sc.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var settings ServerSettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return settings.DownloadLocation, nil
}

// GetWatchFolder fetches the watch folder from the main server
func (sc *SettingsClient) GetWatchFolder() (string, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/settings", sc.mainServerURL, sc.serverID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Server-ID", sc.serverID)
	req.Header.Set("X-MAC-Address", sc.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var settings ServerSettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return settings.WatchFolder, nil
}

// GetTorrentDownloadLocation fetches the torrent download location from the main server
func (sc *SettingsClient) GetTorrentDownloadLocation() (string, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/settings", sc.mainServerURL, sc.serverID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Server-ID", sc.serverID)
	req.Header.Set("X-MAC-Address", sc.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var settings ServerSettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Return torrent download location, fall back to general download location if not set
	if settings.TorrentDownloadLocation != "" {
		log.Printf("Using torrent download location: %s", settings.TorrentDownloadLocation)
		return settings.TorrentDownloadLocation, nil
	}

	// Fallback to general download location
	if settings.DownloadLocation != "" {
		log.Printf("Torrent download location not set, falling back to: %s", settings.DownloadLocation)
		return settings.DownloadLocation, nil
	}

	return "", fmt.Errorf("no torrent download location configured")
}

// fetchSettings is a helper that fetches and caches the settings response
func (sc *SettingsClient) fetchSettings() (*ServerSettingsResponse, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/settings", sc.mainServerURL, sc.serverID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-Server-ID", sc.serverID)
	req.Header.Set("X-MAC-Address", sc.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var settings ServerSettingsResponse
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &settings, nil
}

// GetRosettaBridgePath returns the path of the RosettaBridge library location, or empty if none configured
func (sc *SettingsClient) GetRosettaBridgePath() (string, error) {
	settings, err := sc.fetchSettings()
	if err != nil {
		return "", err
	}

	for _, lib := range settings.LibraryLocations {
		if lib.IsActive && lib.LocationType == "rosettabridge" {
			return lib.Path, nil
		}
	}

	return "", nil // No RosettaBridge configured - not an error
}

// GetAutoCleanupAfterIngestion returns whether auto-cleanup is enabled for this server
func (sc *SettingsClient) GetAutoCleanupAfterIngestion() (bool, error) {
	settings, err := sc.fetchSettings()
	if err != nil {
		return false, err
	}

	return settings.AutoCleanupAfterIngestion, nil
}

// GetLibraryLocationsDetailed returns full library location details including type
func (sc *SettingsClient) GetLibraryLocationsDetailed() ([]LibraryLocation, error) {
	settings, err := sc.fetchSettings()
	if err != nil {
		return nil, err
	}

	return settings.LibraryLocations, nil
}

// ReportIngestion sends an ingestion status update to the main server
func (sc *SettingsClient) ReportIngestion(packageID, infoHash, downloadPath, rbPath, status, errorMsg string) error {
	url := fmt.Sprintf("%s/api/v1/servers/%s/ingestion-status", sc.mainServerURL, sc.serverID)

	body := map[string]string{
		"package_id":        packageID,
		"info_hash":         infoHash,
		"download_path":     downloadPath,
		"rosettabridge_path": rbPath,
		"status":            status,
		"error_message":     errorMsg,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", sc.serverID)
	req.Header.Set("X-MAC-Address", sc.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to report ingestion: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	log.Printf("[ingestion] Reported status '%s' for package %s to main server", status, packageID)
	return nil
}

// CanonicalXMLResult contains the canonical torrent and XML files returned by the main server
// for a DCP identified by CPL UUID.
type CanonicalXMLResult struct {
	PackageID    string
	AssetMapUUID string
	InfoHash     string
	TorrentFile  []byte
	Files        map[string][]byte // relative filename -> file bytes
}

// GetCanonicalXML asks the main server for the canonical XML files and torrent for a DCP
// identified by its CPL UUID. Returns nil if no match found (torrent not yet generated).
// The caller should overwrite its local XML files with these to enable co-seeding.
func (sc *SettingsClient) GetCanonicalXML(cplUUID string) (*CanonicalXMLResult, error) {
	url := fmt.Sprintf("%s/api/v1/servers/%s/canonical-xml", sc.mainServerURL, sc.serverID)

	body, err := json.Marshal(map[string]string{"cpl_uuid": cplUUID})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", sc.serverID)
	req.Header.Set("X-MAC-Address", sc.macAddress)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Not found — torrent not yet generated or CPL not in system
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	var apiResp struct {
		PackageID    string            `json:"package_id"`
		AssetMapUUID string            `json:"assetmap_uuid"`
		InfoHash     string            `json:"info_hash"`
		TorrentFile  string            `json:"torrent_file"`
		Files        map[string]string `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	torrentBytes, err := base64.StdEncoding.DecodeString(apiResp.TorrentFile)
	if err != nil {
		return nil, fmt.Errorf("failed to decode torrent file: %w", err)
	}

	result := &CanonicalXMLResult{
		PackageID:    apiResp.PackageID,
		AssetMapUUID: apiResp.AssetMapUUID,
		InfoHash:     apiResp.InfoHash,
		TorrentFile:  torrentBytes,
		Files:        make(map[string][]byte, len(apiResp.Files)),
	}
	for name, b64 := range apiResp.Files {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			log.Printf("[canonical-xml] Warning: failed to decode file %s: %v", name, err)
			continue
		}
		result.Files[name] = data
	}

	return result, nil
}

// ApplyCanonicalXML writes the canonical XML files to the local DCP directory,
// overwriting any existing files. Only non-MXF files are written.
func ApplyCanonicalXML(localPackagePath string, files map[string][]byte) error {
	for relPath, data := range files {
		// Safety: never write MXF files
		if filepath.Ext(relPath) == ".mxf" {
			continue
		}
		destPath := filepath.Join(localPackagePath, relPath)
		// Ensure subdirectory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create dir for %s: %w", relPath, err)
		}
		if err := ioutil.WriteFile(destPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", relPath, err)
		}
		log.Printf("[canonical-xml] Wrote %s (%d bytes)", relPath, len(data))
	}
	return nil
}
