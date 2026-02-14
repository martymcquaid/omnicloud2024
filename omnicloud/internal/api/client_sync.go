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
	"github.com/omnicloud/omnicloud/pkg/dcp"
)

// DCPPackageMetadata represents complete DCP package metadata for sync
type DCPPackageMetadata struct {
	ID             string                    `json:"id"`
	AssetMapUUID   string                    `json:"assetmap_uuid"`
	PackageName    string                    `json:"package_name"`
	ContentTitle   string                    `json:"content_title"`
	ContentKind    string                    `json:"content_kind"`
	IssueDate      *time.Time                `json:"issue_date,omitempty"`
	Issuer         string                    `json:"issuer"`
	Creator        string                    `json:"creator"`
	AnnotationText string                    `json:"annotation_text"`
	VolumeCount    int                       `json:"volume_count"`
	TotalSizeBytes int64                     `json:"total_size_bytes"`
	FileCount      int                       `json:"file_count"`
	DiscoveredAt   time.Time                 `json:"discovered_at"`
	LastVerified   *time.Time                `json:"last_verified,omitempty"`
	Compositions   []DCPCompositionMetadata  `json:"compositions,omitempty"`
	Assets         []DCPAssetMetadata        `json:"assets,omitempty"`
}

// DCPCompositionMetadata represents CPL metadata
type DCPCompositionMetadata struct {
	ID                     string     `json:"id"`
	CPLUUID                string     `json:"cpl_uuid"`
	ContentTitleText       string     `json:"content_title_text"`
	ContentKind            string     `json:"content_kind"`
	IssueDate              *time.Time `json:"issue_date,omitempty"`
	Issuer                 string     `json:"issuer"`
	Creator                string     `json:"creator"`
	EditRate               string     `json:"edit_rate"`
	FrameRate              string     `json:"frame_rate"`
	ScreenAspectRatio      string     `json:"screen_aspect_ratio"`
	ResolutionWidth        int        `json:"resolution_width"`
	ResolutionHeight       int        `json:"resolution_height"`
	MainSoundConfiguration string     `json:"main_sound_configuration"`
	ReelCount              int        `json:"reel_count"`
	TotalDurationFrames    int        `json:"total_duration_frames"`
}

// DCPAssetMetadata represents asset file metadata
type DCPAssetMetadata struct {
	ID            string `json:"id"`
	AssetUUID     string `json:"asset_uuid"`
	FilePath      string `json:"file_path"`
	FileName      string `json:"file_name"`
	AssetType     string `json:"asset_type"`
	AssetRole     string `json:"asset_role"`
	SizeBytes     int64  `json:"size_bytes"`
	HashAlgorithm string `json:"hash_algorithm"`
	HashValue     string `json:"hash_value"`
}

// DCPMetadataUpdate represents a metadata sync payload
type DCPMetadataUpdate struct {
	ServerID string               `json:"server_id"`
	Packages []DCPPackageMetadata `json:"packages"`
}

// ClientSync handles synchronization from client to main server
type ClientSync struct {
	database        *db.DB
	serverID        uuid.UUID
	mainServerURL   string
	macAddress      string
	registrationKey string
	serverName      string
	serverLocation  string
	softwareVersion string
	scanPath        string
	stopChan        chan struct{}
}

// NewClientSync creates a new client sync service
func NewClientSync(database *db.DB, serverID uuid.UUID, mainServerURL, serverName, serverLocation, registrationKey, softwareVersion, scanPath string) (*ClientSync, error) {
	macAddress, err := dcp.GetMACAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get MAC address: %w", err)
	}

	return &ClientSync{
		database:        database,
		serverID:        serverID,
		mainServerURL:   mainServerURL,
		macAddress:      macAddress,
		registrationKey: registrationKey,
		serverName:      serverName,
		serverLocation:  serverLocation,
		softwareVersion: softwareVersion,
		scanPath:        scanPath,
		stopChan:        make(chan struct{}),
	}, nil
}

// Start begins periodic synchronization with main server
func (cs *ClientSync) Start() {
	log.Printf("Client sync service started (Main Server: %s)", cs.mainServerURL)

	// Register with main server immediately
	go cs.registerWithMainServer()

	// Start periodic sync (every 5 minutes)
	go cs.periodicSync()
}

// Stop stops the sync service
func (cs *ClientSync) Stop() {
	close(cs.stopChan)
	log.Println("Client sync service stopped")
}

// registerWithMainServer registers this client with the main server
func (cs *ClientSync) registerWithMainServer() {
	// Calculate storage capacity from scan path
	var storageCapacityTB float64
	if cs.scanPath != "" {
		totalSize, err := dcp.CalculateDirectorySize(cs.scanPath)
		if err != nil {
			log.Printf("Warning: Could not calculate storage size: %v", err)
		} else {
			storageCapacityTB = float64(totalSize) / (1024 * 1024 * 1024 * 1024) // bytes to TB
		}
	}

	// Detect public IP
	publicIP := dcp.GetPublicIP()
	apiURL := ""
	if publicIP != "" {
		apiURL = fmt.Sprintf("http://%s:10858", publicIP)
	}

	registration := ServerRegistration{
		Name:              cs.serverName,
		Location:          cs.serverLocation,
		APIURL:            apiURL,
		MACAddress:        cs.macAddress,
		RegistrationKey:   cs.registrationKey,
		StorageCapacityTB: storageCapacityTB,
		SoftwareVersion:   cs.softwareVersion,
	}

	data, err := json.Marshal(registration)
	if err != nil {
		log.Printf("Error marshaling registration: %v", err)
		return
	}

	url := fmt.Sprintf("%s/api/v1/servers/register", cs.mainServerURL)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Error registering with main server: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		log.Printf("Registration failed with status: %d", resp.StatusCode)
		return
	}

	// Parse response to get server ID and authorization status
	var regResponse struct {
		ID           string `json:"id"`
		Message      string `json:"message"`
		Status       string `json:"status"`
		IsAuthorized bool   `json:"is_authorized"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&regResponse); err == nil {
		if regResponse.ID != "" {
			// Update our server ID if it was assigned by main server
			if parsedID, err := uuid.Parse(regResponse.ID); err == nil {
				cs.serverID = parsedID
			}
		}

		if !regResponse.IsAuthorized {
			log.Printf("⚠️  Server registered with main server but NOT AUTHORIZED yet")
			log.Printf("⚠️  An administrator must authorize this server before it can sync inventory")
			log.Printf("⚠️  Server ID: %s, MAC: %s", cs.serverID, cs.macAddress)
		} else {
			log.Printf("✓ Successfully registered and AUTHORIZED with main server")
			log.Printf("  Storage: %.2f TB, Public IP: %s, MAC: %s", storageCapacityTB, publicIP, cs.macAddress)
		}
	} else {
		log.Printf("Successfully registered with main server (Storage: %.2f TB, IP: %s)", storageCapacityTB, publicIP)
	}
}

// periodicSync syncs inventory with main server periodically
func (cs *ClientSync) periodicSync() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// Also send heartbeat with storage updates every 5 minutes
	heartbeatTicker := time.NewTicker(5 * time.Minute)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ticker.C:
			cs.syncInventory()
			cs.syncDCPMetadata()

		case <-heartbeatTicker.C:
			cs.sendHeartbeat()

		case <-cs.stopChan:
			return
		}
	}
}

// syncInventory sends current inventory to main server
func (cs *ClientSync) syncInventory() {
	log.Println("Syncing inventory with main server...")

	// Get all packages from local database
	query := `
		SELECT p.assetmap_uuid, i.local_path, i.status
		FROM dcp_packages p
		JOIN server_dcp_inventory i ON p.id = i.package_id
		WHERE i.server_id = $1
		ORDER BY p.package_name`

	rows, err := cs.database.Query(query, cs.serverID)
	if err != nil {
		log.Printf("Error querying inventory: %v", err)
		return
	}
	defer rows.Close()

	var packages []InventoryPackage
	for rows.Next() {
		var assetMapUUID uuid.UUID
		var localPath, status string

		if err := rows.Scan(&assetMapUUID, &localPath, &status); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}

		packages = append(packages, InventoryPackage{
			AssetMapUUID: assetMapUUID.String(),
			LocalPath:    localPath,
			Status:       status,
		})
	}

	if len(packages) == 0 {
		log.Println("No packages to sync")
		return
	}

	// Send to main server
	update := InventoryUpdate{
		Packages: packages,
	}

	data, err := json.Marshal(update)
	if err != nil {
		log.Printf("Error marshaling inventory: %v", err)
		return
	}

	url := fmt.Sprintf("%s/api/v1/servers/%s/inventory", cs.mainServerURL, cs.serverID)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		log.Printf("Error creating inventory request: %v", err)
		return
	}

	// Add authentication headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", cs.serverID.String())
	req.Header.Set("X-MAC-Address", cs.macAddress)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error syncing inventory: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		log.Printf("⚠️  Inventory sync BLOCKED - server not authorized by administrator")
		log.Printf("⚠️  Please contact the administrator to authorize this server (ID: %s, MAC: %s)", cs.serverID, cs.macAddress)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("Inventory sync failed with status: %d", resp.StatusCode)
		return
	}

	log.Printf("Successfully synced %d packages with main server", len(packages))
}

// syncDCPMetadata sends complete DCP metadata (packages, compositions, assets) to main server
func (cs *ClientSync) syncDCPMetadata() {
	log.Println("Syncing DCP metadata with main server...")

	// Get all packages with full metadata
	query := `
		SELECT p.id, p.assetmap_uuid, p.package_name, p.content_title, p.content_kind,
		       p.issue_date, p.issuer, p.creator, p.annotation_text, p.volume_count,
		       p.total_size_bytes, p.file_count, p.discovered_at, p.last_verified
		FROM dcp_packages p
		JOIN server_dcp_inventory i ON p.id = i.package_id
		WHERE i.server_id = $1
		ORDER BY p.discovered_at DESC
		LIMIT 100
	`

	rows, err := cs.database.Query(query, cs.serverID)
	if err != nil {
		log.Printf("Error querying DCP metadata: %v", err)
		return
	}
	defer rows.Close()

	var packages []DCPPackageMetadata
	for rows.Next() {
		var pkg DCPPackageMetadata
		var issueDate, lastVerified *time.Time

		err := rows.Scan(
			&pkg.ID, &pkg.AssetMapUUID, &pkg.PackageName, &pkg.ContentTitle, &pkg.ContentKind,
			&issueDate, &pkg.Issuer, &pkg.Creator, &pkg.AnnotationText, &pkg.VolumeCount,
			&pkg.TotalSizeBytes, &pkg.FileCount, &pkg.DiscoveredAt, &lastVerified,
		)
		if err != nil {
			log.Printf("Error scanning DCP metadata: %v", err)
			continue
		}

		if issueDate != nil {
			pkg.IssueDate = issueDate
		}
		if lastVerified != nil {
			pkg.LastVerified = lastVerified
		}

		// Get compositions for this package
		pkg.Compositions = cs.getCompositionsForPackage(pkg.ID)

		// Get assets for this package
		pkg.Assets = cs.getAssetsForPackage(pkg.ID)

		packages = append(packages, pkg)
	}

	if len(packages) == 0 {
		log.Println("No DCP metadata to sync")
		return
	}

	// Send to main server in batches
	batchSize := 10
	for i := 0; i < len(packages); i += batchSize {
		end := i + batchSize
		if end > len(packages) {
			end = len(packages)
		}

		batch := packages[i:end]
		if err := cs.sendDCPMetadataBatch(batch); err != nil {
			log.Printf("Error sending DCP metadata batch: %v", err)
			continue
		}

		log.Printf("Synced metadata for %d packages (batch %d/%d)", len(batch), (i/batchSize)+1, (len(packages)+batchSize-1)/batchSize)
	}

	log.Printf("Successfully synced metadata for %d packages", len(packages))
}

// getCompositionsForPackage retrieves compositions (CPLs) for a package
func (cs *ClientSync) getCompositionsForPackage(packageID string) []DCPCompositionMetadata {
	query := `
		SELECT id, cpl_uuid, content_title_text, content_kind, issue_date, issuer,
		       creator, edit_rate, frame_rate, screen_aspect_ratio, resolution_width,
		       resolution_height, main_sound_configuration, reel_count, total_duration_frames
		FROM dcp_compositions
		WHERE package_id = $1
	`

	rows, err := cs.database.Query(query, packageID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var comps []DCPCompositionMetadata
	for rows.Next() {
		var comp DCPCompositionMetadata
		var issueDate *time.Time

		err := rows.Scan(
			&comp.ID, &comp.CPLUUID, &comp.ContentTitleText, &comp.ContentKind, &issueDate,
			&comp.Issuer, &comp.Creator, &comp.EditRate, &comp.FrameRate, &comp.ScreenAspectRatio,
			&comp.ResolutionWidth, &comp.ResolutionHeight, &comp.MainSoundConfiguration,
			&comp.ReelCount, &comp.TotalDurationFrames,
		)
		if err != nil {
			continue
		}

		if issueDate != nil {
			comp.IssueDate = issueDate
		}

		comps = append(comps, comp)
	}

	return comps
}

// getAssetsForPackage retrieves assets for a package
func (cs *ClientSync) getAssetsForPackage(packageID string) []DCPAssetMetadata {
	query := `
		SELECT id, asset_uuid, file_path, file_name, asset_type, asset_role,
		       size_bytes, hash_algorithm, hash_value
		FROM dcp_assets
		WHERE package_id = $1
		LIMIT 50
	`

	rows, err := cs.database.Query(query, packageID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var assets []DCPAssetMetadata
	for rows.Next() {
		var asset DCPAssetMetadata

		err := rows.Scan(
			&asset.ID, &asset.AssetUUID, &asset.FilePath, &asset.FileName, &asset.AssetType,
			&asset.AssetRole, &asset.SizeBytes, &asset.HashAlgorithm, &asset.HashValue,
		)
		if err != nil {
			continue
		}

		assets = append(assets, asset)
	}

	return assets
}

// sendDCPMetadataBatch sends a batch of DCP metadata to main server
func (cs *ClientSync) sendDCPMetadataBatch(packages []DCPPackageMetadata) error {
	update := DCPMetadataUpdate{
		ServerID: cs.serverID.String(),
		Packages: packages,
	}

	data, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/servers/%s/dcp-metadata", cs.mainServerURL, cs.serverID)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", cs.serverID.String())
	req.Header.Set("X-MAC-Address", cs.macAddress)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("server not authorized")
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	return nil
}

// sendHeartbeat sends periodic heartbeat with updated storage capacity
func (cs *ClientSync) sendHeartbeat() {
	// Calculate current storage capacity
	var storageCapacityTB float64
	if cs.scanPath != "" {
		totalSize, err := dcp.CalculateDirectorySize(cs.scanPath)
		if err != nil {
			log.Printf("Warning: Could not calculate storage size: %v", err)
		} else {
			storageCapacityTB = float64(totalSize) / (1024 * 1024 * 1024 * 1024)
		}
	}

	// Get current package count
	packageCount, _ := cs.database.CountDCPPackages()

	// Update local server record with storage capacity
	_, err := cs.database.Exec(`
		UPDATE servers 
		SET storage_capacity_tb = $1, 
		    software_version = $2,
		    last_seen = $3
		WHERE id = $4
	`, storageCapacityTB, cs.softwareVersion, time.Now(), cs.serverID)
	if err != nil {
		log.Printf("Error updating local server record: %v", err)
	}

	// Send heartbeat to main server
	url := fmt.Sprintf("%s/api/v1/servers/%s/heartbeat", cs.mainServerURL, cs.serverID)
	
	heartbeat := map[string]interface{}{
		"storage_capacity_tb": storageCapacityTB,
		"software_version":    cs.softwareVersion,
		"package_count":       packageCount,
	}

	data, err := json.Marshal(heartbeat)
	if err != nil {
		log.Printf("Error marshaling heartbeat: %v", err)
		return
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Server-ID", cs.serverID.String())
	req.Header.Set("X-MAC-Address", cs.macAddress)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending heartbeat: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		log.Printf("Heartbeat sent: %.2f TB, %d packages", storageCapacityTB, packageCount)
	}
}

