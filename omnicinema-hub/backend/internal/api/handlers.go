package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/omnicloud/omnicloud/internal/db"
	"github.com/omnicloud/omnicloud/internal/torrent"
)

// Response structures
type HealthResponse struct {
	Status  string    `json:"status"`
	Time    time.Time `json:"time"`
	Version string    `json:"version"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

type ServerRegistration struct {
	Name              string  `json:"name"`
	Location          string  `json:"location"`
	APIURL            string  `json:"api_url"`
	MACAddress        string  `json:"mac_address"`
	RegistrationKey   string  `json:"registration_key"`
	StorageCapacityTB float64 `json:"storage_capacity_tb"`
}

type InventoryUpdate struct {
	Packages []InventoryPackage `json:"packages"`
}

type InventoryPackage struct {
	AssetMapUUID string `json:"assetmap_uuid"`
	LocalPath    string `json:"local_path"`
	Status       string `json:"status"`
}

// handleHealth returns server health status
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Test database connection
	if err := s.database.Ping(); err != nil {
		respondError(w, http.StatusServiceUnavailable, "Database unavailable", err.Error())
		return
	}

	response := HealthResponse{
		Status:  "healthy",
		Time:    time.Now(),
		Version: "1.0.0",
	}

	respondJSON(w, http.StatusOK, response)
}

// handleListServers returns all registered servers
func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.database.Query("SELECT id, name, location, api_url, COALESCE(mac_address, ''), COALESCE(is_authorized, false), last_seen, storage_capacity_tb FROM servers ORDER BY name")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query servers", err.Error())
		return
	}
	defer rows.Close()

	var servers []map[string]interface{}
	for rows.Next() {
		var id uuid.UUID
		var name, location, apiURL, macAddress string
		var isAuthorized bool
		var lastSeen *time.Time
		var capacity float64

		if err := rows.Scan(&id, &name, &location, &apiURL, &macAddress, &isAuthorized, &lastSeen, &capacity); err != nil {
			log.Printf("Error scanning server row: %v", err)
			continue
		}

		servers = append(servers, map[string]interface{}{
			"id":                   id,
			"name":                 name,
			"location":             location,
			"api_url":              apiURL,
			"mac_address":          macAddress,
			"is_authorized":        isAuthorized,
			"last_seen":            lastSeen,
			"storage_capacity_tb":  capacity,
		})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"servers": servers,
		"count":   len(servers),
	})
}

// handleRegisterServer registers a new site server with MAC address authentication
func (s *Server) handleRegisterServer(w http.ResponseWriter, r *http.Request) {
	var reg ServerRegistration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	// Validate required fields
	if reg.Name == "" || reg.MACAddress == "" || reg.RegistrationKey == "" {
		respondError(w, http.StatusBadRequest, "Missing required fields", "name, mac_address, and registration_key are required")
		return
	}

	// Check if server already exists by MAC address
	existing, err := s.database.GetServerByMACAddress(reg.MACAddress)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}

	now := time.Now()
	
	if existing != nil {
		// Server exists - verify registration key matches
		if !verifyRegistrationKey(reg.RegistrationKey, existing.RegistrationKeyHash) {
			log.Printf("Unauthorized registration attempt from MAC: %s", reg.MACAddress)
			respondError(w, http.StatusUnauthorized, "Invalid credentials", "MAC address and registration key do not match")
			return
		}

		// Update existing server
		existing.Name = reg.Name
		existing.Location = reg.Location
		existing.APIURL = reg.APIURL
		existing.LastSeen = &now
		existing.StorageCapacityTB = reg.StorageCapacityTB
		existing.IsAuthorized = true

		if err := s.database.UpsertServer(existing); err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to update server", err.Error())
			return
		}

		log.Printf("Server re-registered: %s (MAC: %s, ID: %s)", existing.Name, reg.MACAddress, existing.ID)
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"id":      existing.ID,
			"message": "Server re-registered successfully",
			"status":  "existing",
		})
		return
	}

	// New server - verify registration key matches main server key
	if reg.RegistrationKey != s.registrationKey {
		log.Printf("Invalid registration key from new server: %s (MAC: %s)", reg.Name, reg.MACAddress)
		respondError(w, http.StatusUnauthorized, "Invalid registration key", "The provided registration key is incorrect")
		return
	}

	// Create new server with hashed key
	keyHash := hashRegistrationKey(reg.RegistrationKey)
	server := &db.Server{
		ID:                  uuid.New(),
		Name:                reg.Name,
		Location:            reg.Location,
		APIURL:              reg.APIURL,
		MACAddress:          reg.MACAddress,
		RegistrationKeyHash: keyHash,
		IsAuthorized:        true,
		LastSeen:            &now,
		StorageCapacityTB:   reg.StorageCapacityTB,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := s.database.UpsertServer(server); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to register server", err.Error())
		return
	}

	log.Printf("New server registered: %s (MAC: %s, ID: %s)", server.Name, reg.MACAddress, server.ID)
	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"id":      server.ID,
		"message": "Server registered successfully",
		"status":  "new",
	})
}

// handleHeartbeat updates server last_seen timestamp
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	now := time.Now()
	_, err = s.database.Exec("UPDATE servers SET last_seen = $1, updated_at = $2 WHERE id = $3",
		now, now, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update heartbeat", err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Heartbeat recorded",
	})
}

// handleUpdateInventory updates a server's DCP inventory
func (s *Server) handleUpdateInventory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var update InventoryUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	// Process each package in the inventory
	updated := 0
	for _, pkg := range update.Packages {
		assetMapUUID, err := uuid.Parse(pkg.AssetMapUUID)
		if err != nil {
			log.Printf("Invalid UUID %s: %v", pkg.AssetMapUUID, err)
			continue
		}

		// Get package ID
		dcpPkg, err := s.database.GetDCPPackageByAssetMapUUID(assetMapUUID)
		if err != nil || dcpPkg == nil {
			log.Printf("Package not found: %s", assetMapUUID)
			continue
		}

		// Upsert inventory record
		now := time.Now()
		inv := &db.ServerDCPInventory{
			ID:           uuid.New(),
			ServerID:     serverID,
			PackageID:    dcpPkg.ID,
			LocalPath:    pkg.LocalPath,
			Status:       pkg.Status,
			LastVerified: now,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := s.database.UpsertServerDCPInventory(inv); err != nil {
			log.Printf("Error updating inventory: %v", err)
			continue
		}
		updated++
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Inventory updated",
		"updated": updated,
		"total":   len(update.Packages),
	})
}

// handleTorrentStatus receives and stores torrent status from client servers (hashing queue + seeding/downloading)
func (s *Server) handleTorrentStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var report torrent.TorrentStatusReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	now := time.Now()

	// Process queue items (hashing progress from client). Merge by (package_id, server_id) only.
	// Only store items for packages that exist in main server's dcp_packages (FK); skip unknown packages.
	if len(report.QueueItems) > 0 {
		for _, item := range report.QueueItems {
			packageID, err := uuid.Parse(item.PackageID)
			if err != nil {
				log.Printf("Invalid package ID %s: %v", item.PackageID, err)
				continue
			}

			var packageExists bool
			if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM dcp_packages WHERE id = $1)", packageID).Scan(&packageExists); err != nil || !packageExists {
				continue // package not in main catalog; skip to avoid FK violation
			}

			newID := uuid.New()
			query := `
				INSERT INTO torrent_queue (id, package_id, server_id, status, progress_percent, current_file, started_at, total_size_bytes, hashing_speed_bps, synced_at, queued_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
				ON CONFLICT (package_id, server_id)
				DO UPDATE SET
					status = EXCLUDED.status,
					progress_percent = EXCLUDED.progress_percent,
					current_file = EXCLUDED.current_file,
					started_at = EXCLUDED.started_at,
					total_size_bytes = EXCLUDED.total_size_bytes,
					hashing_speed_bps = EXCLUDED.hashing_speed_bps,
					synced_at = EXCLUDED.synced_at
			`
			_, err = s.db.Exec(query,
				newID,
				packageID,
				serverID,
				item.Status,
				item.ProgressPercent,
				item.CurrentFile,
				item.StartedAt,
				item.TotalSizeBytes,
				item.HashingSpeedBps,
				now,
			)
			if err != nil {
				log.Printf("Error updating queue item: %v", err)
				continue
			}
		}
	}

	// Process torrent status (seeding/downloading)
	if len(report.Torrents) > 0 {
		for _, tr := range report.Torrents {
			if tr.Status == "seeding" || tr.Status == "completed" {
				var torrentID uuid.UUID
				err := s.db.QueryRow("SELECT id FROM dcp_torrents WHERE info_hash = $1", tr.InfoHash).Scan(&torrentID)
				if err != nil {
					log.Printf("Torrent not found for info_hash %s: %v", tr.InfoHash, err)
					continue
				}
				seederID := uuid.New()
				seederQuery := `
					INSERT INTO torrent_seeders (id, torrent_id, server_id, local_path, status, uploaded_bytes, last_announce, created_at, updated_at)
					VALUES ($1, $2, $3, '', $4, $5, $6, $6, $6)
					ON CONFLICT (torrent_id, server_id)
					DO UPDATE SET status = $4, uploaded_bytes = $5, last_announce = $6, updated_at = $6
				`
				_, err = s.db.Exec(seederQuery, seederID, torrentID, serverID, tr.Status, tr.UploadedBytes, now)
				if err != nil {
					log.Printf("Error updating seeder: %v", err)
				}
			}
			if tr.Status == "downloading" {
				transferQuery := `
					UPDATE transfers SET progress_percent = $1, downloaded_bytes = $2, download_speed_bps = $3,
						upload_speed_bps = $4, peers_connected = $5, eta_seconds = $6, updated_at = $7
					WHERE destination_server_id = $8 AND status IN ('downloading', 'active')
					AND EXISTS (SELECT 1 FROM dcp_torrents WHERE info_hash = $9 AND id = transfers.torrent_id)
				`
				_, _ = s.db.Exec(transferQuery,
					tr.Progress, tr.BytesCompleted, tr.DownloadSpeed, tr.UploadSpeed, tr.PeersConnected, tr.ETA,
					now, serverID, tr.InfoHash,
				)
			}
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":       "Status received",
		"queue_items":   len(report.QueueItems),
		"torrent_items": len(report.Torrents),
	})
}

// handleGetServerDCPs returns all DCPs on a specific server
func (s *Server) handleGetServerDCPs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	query := `
		SELECT p.id, p.assetmap_uuid, p.package_name, p.content_title, p.content_kind,
		       i.local_path, i.status, i.last_verified
		FROM dcp_packages p
		JOIN server_dcp_inventory i ON p.id = i.package_id
		WHERE i.server_id = $1
		ORDER BY p.package_name`

	rows, err := s.database.Query(query, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query DCPs", err.Error())
		return
	}
	defer rows.Close()

	var dcps []map[string]interface{}
	for rows.Next() {
		var id, assetMapUUID uuid.UUID
		var packageName, contentTitle, contentKind, localPath, status string
		var lastVerified time.Time

		if err := rows.Scan(&id, &assetMapUUID, &packageName, &contentTitle, &contentKind,
			&localPath, &status, &lastVerified); err != nil {
			log.Printf("Error scanning DCP row: %v", err)
			continue
		}

		dcps = append(dcps, map[string]interface{}{
			"id":              id,
			"assetmap_uuid":   assetMapUUID,
			"package_name":    packageName,
			"content_title":   contentTitle,
			"content_kind":    contentKind,
			"local_path":      localPath,
			"status":          status,
			"last_verified":   lastVerified,
		})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"dcps":  dcps,
		"count": len(dcps),
	})
}

// handleRescanServer sets rescan_requested_at for the given server (main server). Clients poll pending-action and run the scan.
func (s *Server) handleRescanServer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}
	server, err := s.database.GetServer(serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get server", err.Error())
		return
	}
	if server == nil {
		respondError(w, http.StatusNotFound, "Server not found", "")
		return
	}
	if err := s.database.SetRescanRequested(serverID); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to set rescan", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Rescan requested; client will run scan when it checks in",
	})
}

// handleServerScanStatus returns the last scan status for a server (stored when client reports action-done).
func (s *Server) handleServerScanStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}
	result, err := s.database.GetLastScanResult(serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get scan status", err.Error())
		return
	}
	if result == nil || len(result) == 0 {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"server_id": serverID.String(),
			"status":    "unknown",
			"message":   "No scan reported yet",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result)
}

// handlePendingAction returns pending command for the client (e.g. rescan). Called by clients when they check in.
func (s *Server) handlePendingAction(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}
	at, err := s.database.GetRescanRequested(serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to check pending action", err.Error())
		return
	}
	if at != nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"action": "rescan",
		})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"action": nil,
	})
}

// ActionDoneRequest is the body for POST /servers/:id/action-done
type ActionDoneRequest struct {
	Action     string                 `json:"action"`
	Status     string                 `json:"status"`
	Message    string                 `json:"message,omitempty"`
	ScanStatus map[string]interface{} `json:"scan_status,omitempty"`
}

// handleActionDone clears the pending action and stores scan result when client reports rescan complete.
func (s *Server) handleActionDone(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}
	var body ActionDoneRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if body.Action != "rescan" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"message": "ack"})
		return
	}
	var resultJSON []byte
	if len(body.ScanStatus) > 0 {
		resultJSON, _ = json.Marshal(body.ScanStatus)
	} else {
		resultJSON, _ = json.Marshal(map[string]interface{}{
			"status": body.Status,
			"message": body.Message,
		})
	}
	if err := s.database.ClearRescanRequestedAndSetLastScanResult(serverID, resultJSON); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to save scan result", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Action acknowledged",
	})
}

// handleListDCPs returns all DCP packages
func (s *Server) handleListDCPs(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT id, assetmap_uuid, package_name, content_title, content_kind,
		       issuer, total_size_bytes, file_count, discovered_at
		FROM dcp_packages
		ORDER BY discovered_at DESC
		LIMIT 100`

	rows, err := s.database.Query(query)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query DCPs", err.Error())
		return
	}
	defer rows.Close()

	var dcps []map[string]interface{}
	for rows.Next() {
		var id, assetMapUUID uuid.UUID
		var packageName, contentTitle, contentKind, issuer string
		var totalSize int64
		var fileCount int
		var discoveredAt time.Time

		if err := rows.Scan(&id, &assetMapUUID, &packageName, &contentTitle, &contentKind,
			&issuer, &totalSize, &fileCount, &discoveredAt); err != nil {
			log.Printf("Error scanning DCP row: %v", err)
			continue
		}

		dcps = append(dcps, map[string]interface{}{
			"id":               id,
			"assetmap_uuid":    assetMapUUID,
			"package_name":     packageName,
			"content_title":    contentTitle,
			"content_kind":     contentKind,
			"issuer":           issuer,
			"total_size_bytes": totalSize,
			"file_count":       fileCount,
			"discovered_at":    discoveredAt,
		})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"dcps":  dcps,
		"count": len(dcps),
	})
}

// handleGetDCP returns detailed information about a specific DCP
func (s *Server) handleGetDCP(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	dcpUUID, err := uuid.Parse(vars["uuid"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid DCP UUID", err.Error())
		return
	}

	pkg, err := s.database.GetDCPPackageByAssetMapUUID(dcpUUID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query DCP", err.Error())
		return
	}
	if pkg == nil {
		respondError(w, http.StatusNotFound, "DCP not found", "")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"id":               pkg.ID,
		"assetmap_uuid":    pkg.AssetMapUUID,
		"package_name":     pkg.PackageName,
		"content_title":    pkg.ContentTitle,
		"content_kind":     pkg.ContentKind,
		"issue_date":       pkg.IssueDate,
		"issuer":           pkg.Issuer,
		"creator":          pkg.Creator,
		"volume_count":     pkg.VolumeCount,
		"total_size_bytes": pkg.TotalSizeBytes,
		"file_count":       pkg.FileCount,
		"discovered_at":    pkg.DiscoveredAt,
		"last_verified":    pkg.LastVerified,
	})
}

// Helper functions
func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, error string, message string) {
	respondJSON(w, status, ErrorResponse{
		Error:   error,
		Message: message,
	})
}

// hashRegistrationKey creates a SHA256 hash of the registration key
func hashRegistrationKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// verifyRegistrationKey checks if a key matches the stored hash
func verifyRegistrationKey(key, hash string) bool {
	return hashRegistrationKey(key) == hash
}
