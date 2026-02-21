package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/omnicloud/omnicloud/internal/relay"
	"github.com/omnicloud/omnicloud/internal/db"
	"github.com/omnicloud/omnicloud/internal/updater"
	ws "github.com/omnicloud/omnicloud/internal/websocket"
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
	SoftwareVersion   string  `json:"software_version,omitempty"`
}

type InventoryUpdate struct {
	Packages []InventoryPackage `json:"packages"`
}

type InventoryPackage struct {
	AssetMapUUID string `json:"assetmap_uuid"`
	LocalPath    string `json:"local_path"`
	Status       string `json:"status"`
}

// TorrentStatusReport represents the status report sent from clients
type TorrentStatusReport struct {
	ServerID    string                 `json:"server_id"`
	Timestamp   time.Time              `json:"timestamp"`
	Torrents    []TorrentStatusItem    `json:"torrents"`
	QueueItems  []QueueStatusItem      `json:"queue_items,omitempty"`
	QueueStats  map[string]int         `json:"queue_stats,omitempty"`
	IsFullSync  bool                   `json:"is_full_sync,omitempty"`
	// NAT/relay status
	IsBehindNAT     bool `json:"is_behind_nat,omitempty"`
	RelayRegistered bool `json:"relay_registered,omitempty"`
}

// TorrentStatusItem represents status for a single torrent
type TorrentStatusItem struct {
	InfoHash       string  `json:"info_hash"`
	Status         string  `json:"status"` // 'verifying', 'seeding', 'downloading', 'completed', 'error'
	IsLoaded       bool    `json:"is_loaded"`
	IsSeeding      bool    `json:"is_seeding"`
	IsDownloading  bool    `json:"is_downloading"`
	BytesCompleted int64   `json:"bytes_completed"`
	BytesTotal     int64   `json:"bytes_total"`
	Progress       float64 `json:"progress"`
	PiecesCompleted int    `json:"pieces_completed"`
	PiecesTotal     int    `json:"pieces_total"`
	DownloadSpeed  int64   `json:"download_speed_bps"`
	UploadSpeed    int64   `json:"upload_speed_bps"`
	UploadedBytes  int64   `json:"uploaded_bytes"`
	PeersConnected int     `json:"peers_connected"`
	ETA            int     `json:"eta_seconds"`
	ErrorMessage   string  `json:"error_message,omitempty"`
	AnnouncedToTracker bool `json:"announced_to_tracker"`
	LastAnnounceAttempt *time.Time `json:"last_announce_attempt,omitempty"`
	LastAnnounceSuccess *time.Time `json:"last_announce_success,omitempty"`
	AnnounceError  string  `json:"announce_error,omitempty"`
}

// QueueStatusItem represents status for a single queue item
type QueueStatusItem struct {
	ID              string    `json:"id"`
	PackageID       string    `json:"package_id"`
	AssetMapUUID    string    `json:"assetmap_uuid,omitempty"`
	Status          string    `json:"status"`
	ProgressPercent float64   `json:"progress_percent"`
	CurrentFile     string    `json:"current_file,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	TotalSizeBytes  int64     `json:"total_size_bytes,omitempty"`
	HashingSpeedBps int64     `json:"hashing_speed_bps,omitempty"`
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
	rows, err := s.database.Query("SELECT id, name, COALESCE(display_name, ''), location, api_url, COALESCE(mac_address, ''), COALESCE(is_authorized, false), last_seen, storage_capacity_tb, COALESCE(software_version, ''), COALESCE(upgrade_status, 'idle'), target_version FROM servers ORDER BY name")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query servers", err.Error())
		return
	}
	defer rows.Close()

	var servers []map[string]interface{}
	for rows.Next() {
		var id uuid.UUID
		var name, displayName, location, apiURL, macAddress string
		var isAuthorized bool
		var lastSeen *time.Time
		var capacity float64
		var softwareVersion, upgradeStatus string
		var targetVersion *string

		if err := rows.Scan(&id, &name, &displayName, &location, &apiURL, &macAddress, &isAuthorized, &lastSeen, &capacity, &softwareVersion, &upgradeStatus, &targetVersion); err != nil {
			log.Printf("Error scanning server row: %v", err)
			continue
		}

		server := map[string]interface{}{
			"id":                   id,
			"name":                 name,
			"display_name":         displayName,
			"location":             location,
			"api_url":              apiURL,
			"mac_address":          macAddress,
			"is_authorized":        isAuthorized,
			"storage_capacity_tb":  capacity,
			"software_version":     softwareVersion,
			"upgrade_status":       upgradeStatus,
		}

		if lastSeen != nil {
			server["last_seen"] = lastSeen
		}
		
		if targetVersion != nil {
			server["target_version"] = *targetVersion
		}

		servers = append(servers, server)
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

		// Update software version if provided
		if reg.SoftwareVersion != "" {
			query := `UPDATE servers SET software_version = $1, last_version_check = $2 WHERE id = $3`
			s.database.DB.Exec(query, reg.SoftwareVersion, now, existing.ID)
		}

		if err := s.database.UpsertServer(existing); err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to update server", err.Error())
			return
		}

		log.Printf("Server re-registered: %s (MAC: %s, ID: %s, Version: %s, Authorized: %v)", existing.Name, reg.MACAddress, existing.ID, reg.SoftwareVersion, existing.IsAuthorized)
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"id":            existing.ID,
			"message":       "Server re-registered successfully",
			"status":        "existing",
			"is_authorized": existing.IsAuthorized,
		})
		return
	}

	// New server - verify registration key matches main server key
	if reg.RegistrationKey != s.registrationKey {
		log.Printf("Invalid registration key from new server: %s (MAC: %s)", reg.Name, reg.MACAddress)
		respondError(w, http.StatusUnauthorized, "Invalid registration key", "The provided registration key is incorrect")
		return
	}

	// Create new server with hashed key - NOT AUTHORIZED BY DEFAULT
	keyHash := hashRegistrationKey(reg.RegistrationKey)
	server := &db.Server{
		ID:                  uuid.New(),
		Name:                reg.Name,
		Location:            reg.Location,
		APIURL:              reg.APIURL,
		MACAddress:          reg.MACAddress,
		RegistrationKeyHash: keyHash,
		IsAuthorized:        false, // Must be authorized by admin
		LastSeen:            &now,
		StorageCapacityTB:   reg.StorageCapacityTB,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	if err := s.database.UpsertServer(server); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to register server", err.Error())
		return
	}

	log.Printf("New server registered (AWAITING AUTHORIZATION): %s (MAC: %s, ID: %s)", server.Name, reg.MACAddress, server.ID)
	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"id":            server.ID,
		"message":       "Server registered successfully - awaiting administrator authorization",
		"status":        "pending_authorization",
		"is_authorized": false,
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

	// Parse optional body with storage/version updates
	var heartbeat struct {
		StorageCapacityTB float64 `json:"storage_capacity_tb"`
		SoftwareVersion   string  `json:"software_version"`
		PackageCount      int     `json:"package_count"`
	}
	
	// Body is optional, just update last_seen if not provided
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&heartbeat)
	}

	now := time.Now()
	
	// Update with storage capacity and software version if provided
	if heartbeat.StorageCapacityTB > 0 || heartbeat.SoftwareVersion != "" {
		_, err = s.database.Exec(`
			UPDATE servers 
			SET last_seen = $1, 
			    updated_at = $2,
			    storage_capacity_tb = COALESCE(NULLIF($3, 0), storage_capacity_tb),
			    software_version = COALESCE(NULLIF($4, ''), software_version)
			WHERE id = $5
		`, now, now, heartbeat.StorageCapacityTB, heartbeat.SoftwareVersion, serverID)
	} else {
		_, err = s.database.Exec("UPDATE servers SET last_seen = $1, updated_at = $2 WHERE id = $3",
			now, now, serverID)
	}
	
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update heartbeat", err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Heartbeat recorded",
	})
}

// handleUpdateServer updates server configuration (including authorization status)
func (s *Server) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var update struct {
		Name              *string  `json:"name"`
		DisplayName       *string  `json:"display_name"`
		Location          *string  `json:"location"`
		IsAuthorized      *bool    `json:"is_authorized"`
		StorageCapacityTB *float64 `json:"storage_capacity_tb"`
	}

	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	// Build dynamic update query (display_name is user-defined; name is device-reported)
	query := "UPDATE servers SET updated_at = $1"
	args := []interface{}{time.Now()}
	argPos := 2

	if update.Name != nil {
		query += fmt.Sprintf(", name = $%d", argPos)
		args = append(args, *update.Name)
		argPos++
	}

	if update.DisplayName != nil {
		query += fmt.Sprintf(", display_name = $%d", argPos)
		args = append(args, *update.DisplayName)
		argPos++
	}

	if update.Location != nil {
		query += fmt.Sprintf(", location = $%d", argPos)
		args = append(args, *update.Location)
		argPos++
	}

	if update.IsAuthorized != nil {
		query += fmt.Sprintf(", is_authorized = $%d", argPos)
		args = append(args, *update.IsAuthorized)
		argPos++
		
		if *update.IsAuthorized {
			log.Printf("Server %s has been AUTHORIZED", serverID)
		} else {
			log.Printf("Server %s has been UNAUTHORIZED", serverID)
		}
	}

	if update.StorageCapacityTB != nil {
		query += fmt.Sprintf(", storage_capacity_tb = $%d", argPos)
		args = append(args, *update.StorageCapacityTB)
		argPos++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argPos)
	args = append(args, serverID)

	result, err := s.database.DB.Exec(query, args...)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update server", err.Error())
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondError(w, http.StatusNotFound, "Server not found", "No server with that ID")
		return
	}

	s.logActivity(r, "server.update", "servers", "server", serverID.String(), "", "", "success")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Server updated successfully",
	})
}

// handleDeleteServer removes a server from the system
func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	// First, remove all inventory entries for this server
	_, err = s.database.DB.Exec("DELETE FROM server_dcp_inventory WHERE server_id = $1", serverID)
	if err != nil {
		log.Printf("Warning: failed to delete inventory for server %s: %v", serverID, err)
	}

	// Delete the server
	result, err := s.database.DB.Exec("DELETE FROM servers WHERE id = $1", serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to delete server", err.Error())
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondError(w, http.StatusNotFound, "Server not found", "No server with that ID")
		return
	}

	log.Printf("Server %s has been DELETED", serverID)
	s.logActivity(r, "server.delete", "servers", "server", serverID.String(), "", "", "success")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Server deleted successfully",
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

	notFound := len(update.Packages) - updated
	if notFound > 0 {
		log.Printf("[inventory-sync] Server %s: updated %d/%d inventory entries (%d packages not found on main server - metadata may need to sync first)",
			serverID, updated, len(update.Packages), notFound)
	} else {
		log.Printf("[inventory-sync] Server %s: updated %d/%d inventory entries", serverID, updated, len(update.Packages))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Inventory updated",
		"updated": updated,
		"total":   len(update.Packages),
	})
}

// handleTorrentStatus receives and stores torrent status from client servers
func (s *Server) handleTorrentStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var report TorrentStatusReport
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	now := time.Now()

	// On full sync (first report after client startup), clear ALL old queue entries
	// for this server. The report contains the complete current state.
	if report.IsFullSync {
		log.Printf("[torrent-status] Full sync from server %s - clearing old queue entries", serverID)
		_, err := s.db.Exec("DELETE FROM torrent_queue WHERE server_id = $1", serverID)
		if err != nil {
			log.Printf("Error clearing old queue entries: %v", err)
		}
	}

	// Process queue items (hashing progress)
	if len(report.QueueItems) > 0 {
		for _, item := range report.QueueItems {
			queueID, err := uuid.Parse(item.ID)
			if err != nil {
				log.Printf("Invalid queue ID %s: %v", item.ID, err)
				continue
			}

			// Resolve package_id: prefer assetmap_uuid lookup (cross-server),
			// fall back to direct package_id if assetmap_uuid not provided
			var packageID uuid.UUID
			if item.AssetMapUUID != "" {
				assetMapUUID, err := uuid.Parse(item.AssetMapUUID)
				if err != nil {
					log.Printf("Invalid assetmap UUID %s: %v", item.AssetMapUUID, err)
					continue
				}
				// Look up the main server's package_id by assetmap_uuid
				err = s.db.QueryRow("SELECT id FROM dcp_packages WHERE assetmap_uuid = $1", assetMapUUID).Scan(&packageID)
				if err != nil {
					// Package not synced to main server yet - skip silently
					continue
				}
			} else {
				packageID, err = uuid.Parse(item.PackageID)
				if err != nil {
					log.Printf("Invalid package ID %s: %v", item.PackageID, err)
					continue
				}
			}

			// Upsert torrent_queue with progress from client
			query := `
				INSERT INTO torrent_queue (id, package_id, server_id, status, progress_percent, current_file, started_at, total_size_bytes, hashing_speed_bps, synced_at, queued_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
				ON CONFLICT (package_id, server_id)
				DO UPDATE SET
					status = $4,
					progress_percent = $5,
					current_file = $6,
					started_at = $7,
					total_size_bytes = $8,
					hashing_speed_bps = $9,
					synced_at = $10
			`
			_, err = s.db.Exec(query,
				queueID,
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
		for _, torrent := range report.Torrents {
			// Update torrent_seeders if this is seeding
			if torrent.Status == "seeding" || torrent.Status == "completed" {
				// Get torrent_id from info_hash
				var torrentID uuid.UUID
				err := s.db.QueryRow("SELECT id FROM dcp_torrents WHERE info_hash = $1", torrent.InfoHash).Scan(&torrentID)
				if err != nil {
					log.Printf("Torrent not found for info_hash %s: %v", torrent.InfoHash, err)
					continue
				}

				// Upsert seeder status
				seederQuery := `
					INSERT INTO torrent_seeders (id, torrent_id, server_id, local_path, status, uploaded_bytes, last_announce, created_at, updated_at)
					VALUES ($1, $2, $3, '', $4, $5, $6, $6, $6)
					ON CONFLICT (torrent_id, server_id)
					DO UPDATE SET
						status = $4,
						uploaded_bytes = $5,
						last_announce = $6,
						updated_at = $6
				`
				_, err = s.db.Exec(seederQuery, uuid.New().String(), torrentID, serverID, torrent.Status, torrent.UploadedBytes, now)
				if err != nil {
					log.Printf("Error updating seeder: %v", err)
				}
			}

			// Update transfers for active downloads (downloading, checking, verifying, paused, error, completed)
			if torrent.Status == "paused" {
				// Client reports torrent is paused — update progress but keep/confirm paused status.
				// Only touch transfers that are paused or were just set to pause by main server.
				pausedQuery := `
					UPDATE transfers SET
						progress_percent = $1,
						downloaded_bytes = $2,
						download_speed_bps = 0,
						upload_speed_bps = 0,
						peers_connected = 0,
						eta_seconds = NULL,
						status = 'paused',
						updated_at = $3
					WHERE destination_server_id = $4
					AND status IN ('paused', 'downloading', 'checking')
					AND EXISTS (SELECT 1 FROM dcp_torrents WHERE info_hash = $5 AND id = transfers.torrent_id)
				`
				_, err := s.db.Exec(pausedQuery, torrent.Progress, torrent.BytesCompleted, now, serverID, torrent.InfoHash)
				if err != nil {
					log.Printf("Error updating transfer to paused: %v", err)
				}
			} else if torrent.Status == "downloading" || torrent.Status == "verifying" || torrent.Status == "checking" {
				// Map reported status to transfer status: "checking" stays as "checking", "downloading"/"verifying" become "downloading"
				transferStatus := torrent.Status
				if transferStatus == "verifying" {
					transferStatus = "downloading"
				}
				transferQuery := `
					UPDATE transfers SET
						progress_percent = $1,
						downloaded_bytes = $2,
						download_speed_bps = $3,
						upload_speed_bps = $4,
						peers_connected = $5,
						eta_seconds = $6,
						status = CASE WHEN status IN ('queued', 'checking', 'downloading') THEN $10 ELSE status END,
						started_at = CASE WHEN started_at IS NULL THEN $7 ELSE started_at END,
						updated_at = $7
					WHERE destination_server_id = $8
					AND status IN ('downloading', 'checking', 'active', 'queued')
					AND EXISTS (SELECT 1 FROM dcp_torrents WHERE info_hash = $9 AND id = transfers.torrent_id)
				`
				_, err := s.db.Exec(transferQuery,
					torrent.Progress,
					torrent.BytesCompleted,
					torrent.DownloadSpeed,
					torrent.UploadSpeed,
					torrent.PeersConnected,
					torrent.ETA,
					now,
					serverID,
					torrent.InfoHash,
					transferStatus,
				)
				if err != nil {
					log.Printf("Error updating transfer: %v", err)
				}
			} else if torrent.Status == "error" {
				// Mark transfer as errored with the error message from the client
				errorQuery := `
					UPDATE transfers SET
						status = 'error',
						error_message = $1,
						download_speed_bps = 0,
						upload_speed_bps = 0,
						updated_at = $2
					WHERE destination_server_id = $3
					AND status IN ('downloading', 'checking', 'active', 'queued')
					AND EXISTS (SELECT 1 FROM dcp_torrents WHERE info_hash = $4 AND id = transfers.torrent_id)
				`
				_, err := s.db.Exec(errorQuery, torrent.ErrorMessage, now, serverID, torrent.InfoHash)
				if err != nil {
					log.Printf("Error updating transfer to error: %v", err)
				}
			} else if torrent.Status == "completed" && torrent.Progress >= 100 {
				// Mark transfer as completed
				completedQuery := `
					UPDATE transfers SET
						status = 'completed',
						progress_percent = 100,
						downloaded_bytes = $1,
						download_speed_bps = 0,
						upload_speed_bps = 0,
						completed_at = $2,
						updated_at = $2
					WHERE destination_server_id = $3
					AND status IN ('downloading', 'checking', 'active', 'queued')
					AND EXISTS (SELECT 1 FROM dcp_torrents WHERE info_hash = $4 AND id = transfers.torrent_id)
				`
				_, err := s.db.Exec(completedQuery, torrent.BytesTotal, now, serverID, torrent.InfoHash)
				if err != nil {
					log.Printf("Error updating transfer to completed: %v", err)
				}
			}

			// Upsert detailed torrent stats for all torrents (verifying, seeding, downloading)
			statsQuery := `
				INSERT INTO server_torrent_stats (
					server_id, info_hash, status, is_loaded, is_seeding, is_downloading,
					bytes_completed, bytes_total, progress_percent, pieces_completed, pieces_total,
					download_speed_bps, upload_speed_bps, uploaded_bytes, peers_connected, eta_seconds,
					announced_to_tracker, last_announce_attempt, last_announce_success, announce_error, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
				ON CONFLICT (server_id, info_hash)
				DO UPDATE SET
					status = $3,
					is_loaded = $4,
					is_seeding = $5,
					is_downloading = $6,
					bytes_completed = $7,
					bytes_total = $8,
					progress_percent = $9,
					pieces_completed = $10,
					pieces_total = $11,
					download_speed_bps = $12,
					upload_speed_bps = $13,
					uploaded_bytes = $14,
					peers_connected = $15,
					eta_seconds = $16,
					announced_to_tracker = $17,
					last_announce_attempt = $18,
					last_announce_success = $19,
					announce_error = $20,
					updated_at = $21
			`
			_, err = s.db.Exec(statsQuery,
				serverID, torrent.InfoHash, torrent.Status, torrent.IsLoaded, torrent.IsSeeding, torrent.IsDownloading,
				torrent.BytesCompleted, torrent.BytesTotal, torrent.Progress, torrent.PiecesCompleted, torrent.PiecesTotal,
				torrent.DownloadSpeed, torrent.UploadSpeed, torrent.UploadedBytes, torrent.PeersConnected, torrent.ETA,
				torrent.AnnouncedToTracker, torrent.LastAnnounceAttempt, torrent.LastAnnounceSuccess, torrent.AnnounceError, now,
			)
			if err != nil {
				log.Printf("Error updating torrent stats for %s: %v", torrent.InfoHash, err)
			}
		}
	}

	// Update NAT/relay status if included in report
	if report.IsBehindNAT || report.RelayRegistered {
		_, natErr := s.db.Exec(`
			UPDATE servers SET
				is_behind_nat = $1,
				relay_registered = $2,
				nat_last_checked = $3
			WHERE id = $4
		`, report.IsBehindNAT, report.RelayRegistered, now, serverID)
		if natErr != nil {
			log.Printf("[torrent-status] Error updating NAT status for server %s: %v", serverID, natErr)
		} else if report.IsBehindNAT {
			log.Printf("[torrent-status] Server %s: behind_nat=%v relay_registered=%v", serverID, report.IsBehindNAT, report.RelayRegistered)
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":      "Status received",
		"queue_items":  len(report.QueueItems),
		"torrent_items": len(report.Torrents),
	})
}

// handleNATCheck probes whether a client server's torrent data port is reachable.
// Called by client servers to detect if they are behind NAT/firewall.
func (s *Server) handleNATCheck(w http.ResponseWriter, r *http.Request) {
	relay.HandleNATCheck(w, r)
}

// handleDCPMetadata receives and stores complete DCP metadata from client servers
func (s *Server) handleDCPMetadata(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var update DCPMetadataUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	packagesProcessed := 0
	compositionsProcessed := 0
	assetsProcessed := 0

	// Process each package
	for _, pkg := range update.Packages {
		pkgID, err := uuid.Parse(pkg.ID)
		if err != nil {
			log.Printf("Invalid package ID %s: %v", pkg.ID, err)
			continue
		}

		assetMapUUID, err := uuid.Parse(pkg.AssetMapUUID)
		if err != nil {
			log.Printf("Invalid assetmap UUID %s: %v", pkg.AssetMapUUID, err)
			continue
		}

		// Upsert package
		pkgQuery := `
			INSERT INTO dcp_packages (
				id, assetmap_uuid, package_name, content_title, content_kind,
				issue_date, issuer, creator, annotation_text, volume_count,
				total_size_bytes, file_count, discovered_at, last_verified,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (assetmap_uuid) DO UPDATE SET
				package_name = EXCLUDED.package_name,
				content_title = EXCLUDED.content_title,
				content_kind = EXCLUDED.content_kind,
				issue_date = EXCLUDED.issue_date,
				issuer = EXCLUDED.issuer,
				creator = EXCLUDED.creator,
				annotation_text = EXCLUDED.annotation_text,
				volume_count = EXCLUDED.volume_count,
				total_size_bytes = EXCLUDED.total_size_bytes,
				file_count = EXCLUDED.file_count,
				last_verified = EXCLUDED.last_verified,
				updated_at = CURRENT_TIMESTAMP
		`

		now := time.Now()
		_, err = s.db.Exec(pkgQuery,
			pkgID, assetMapUUID, pkg.PackageName, pkg.ContentTitle, pkg.ContentKind,
			pkg.IssueDate, pkg.Issuer, pkg.Creator, pkg.AnnotationText, pkg.VolumeCount,
			pkg.TotalSizeBytes, pkg.FileCount, pkg.DiscoveredAt, pkg.LastVerified,
			now, now,
		)
		if err != nil {
			log.Printf("Error upserting package %s: %v", pkg.PackageName, err)
			continue
		}
		packagesProcessed++

		// Query to get the actual package ID (in case it was a conflict and existing ID differs)
		var actualPkgID string
		err = s.db.QueryRow("SELECT id FROM dcp_packages WHERE assetmap_uuid = $1", assetMapUUID.String()).Scan(&actualPkgID)
		if err != nil {
			log.Printf("Error resolving package ID for assetmap_uuid %s: %v", assetMapUUID, err)
			continue
		}
		actualPkgUUID, _ := uuid.Parse(actualPkgID)

		// Process compositions
		for _, comp := range pkg.Compositions {
			compID, _ := uuid.Parse(comp.ID)
			cplUUID, _ := uuid.Parse(comp.CPLUUID)

			compQuery := `
				INSERT INTO dcp_compositions (
					id, package_id, cpl_uuid, content_title_text, content_kind,
					issue_date, issuer, creator, edit_rate, frame_rate,
					screen_aspect_ratio, resolution_width, resolution_height,
					main_sound_configuration, reel_count, total_duration_frames,
					created_at, updated_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
				ON CONFLICT (package_id, cpl_uuid) DO UPDATE SET
					content_title_text = EXCLUDED.content_title_text,
					content_kind = EXCLUDED.content_kind,
					updated_at = CURRENT_TIMESTAMP
			`

			_, err = s.db.Exec(compQuery,
				compID, actualPkgUUID, cplUUID, comp.ContentTitleText, comp.ContentKind,
				comp.IssueDate, comp.Issuer, comp.Creator, comp.EditRate, comp.FrameRate,
				comp.ScreenAspectRatio, comp.ResolutionWidth, comp.ResolutionHeight,
				comp.MainSoundConfiguration, comp.ReelCount, comp.TotalDurationFrames,
				now, now,
			)
			if err != nil {
				log.Printf("Error upserting composition: %v", err)
				continue
			}
			compositionsProcessed++
		}

		// Process assets
		for _, asset := range pkg.Assets {
			assetID, _ := uuid.Parse(asset.ID)
			assetUUID, _ := uuid.Parse(asset.AssetUUID)

			assetQuery := `
				INSERT INTO dcp_assets (
					id, package_id, asset_uuid, file_path, file_name,
					asset_type, asset_role, size_bytes, hash_algorithm, hash_value,
					created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
				ON CONFLICT (package_id, asset_uuid) DO UPDATE SET
					file_path = EXCLUDED.file_path,
					file_name = EXCLUDED.file_name,
					size_bytes = EXCLUDED.size_bytes
			`

			_, err = s.db.Exec(assetQuery,
				assetID, actualPkgUUID, assetUUID, asset.FilePath, asset.FileName,
				asset.AssetType, asset.AssetRole, asset.SizeBytes, asset.HashAlgorithm, asset.HashValue,
				now,
			)
			if err != nil {
				log.Printf("Error upserting asset: %v", err)
				continue
			}
			assetsProcessed++
		}
	}

	// Count total packages in database after sync
	var totalPackages int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM dcp_packages").Scan(&totalPackages); err == nil {
		log.Printf("[metadata-sync] DCP metadata sync from server %s: processed %d/%d packages, %d compositions, %d assets (total in DB now: %d)",
			serverID, packagesProcessed, len(update.Packages), compositionsProcessed, assetsProcessed, totalPackages)
	} else {
		log.Printf("[metadata-sync] DCP metadata sync from server %s: processed %d/%d packages, %d compositions, %d assets",
			serverID, packagesProcessed, len(update.Packages), compositionsProcessed, assetsProcessed)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":      "Metadata received",
		"packages":     packagesProcessed,
		"compositions": compositionsProcessed,
		"assets":       assetsProcessed,
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

// handleListDCPs returns DCP packages available on at least one server.
// Supports query params: search, content_kind, server_ids (comma-separated UUIDs),
// limit (0 = no limit, default), offset (default 0).
func (s *Server) handleListDCPs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	search := strings.TrimSpace(q.Get("search"))
	contentKind := strings.TrimSpace(q.Get("content_kind"))
	serverIDsParam := strings.TrimSpace(q.Get("server_ids"))

	// limit=0 (or unset) means no limit; explicit positive value paginates
	limit := 0
	offset := 0
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if v, err := strconv.Atoi(q.Get("offset")); err == nil && v >= 0 {
		offset = v
	}

	var args []interface{}
	argIdx := 1

	// Base: DCPs that exist on at least one server (online inventory) or have an active transfer
	baseWhere := `dp.id IN (
		SELECT package_id FROM server_dcp_inventory WHERE status = 'online'
		UNION
		SELECT dt.package_id FROM transfers t
		JOIN dcp_torrents dt ON dt.id = t.torrent_id
		WHERE t.status IN ('queued','downloading','checking','paused','error','failed')
	)`

	filterClause := baseWhere

	// Filter by specific servers: only show DCPs present on at least one of the given servers
	if serverIDsParam != "" {
		var placeholders []string
		for _, raw := range strings.Split(serverIDsParam, ",") {
			sid := strings.TrimSpace(raw)
			if _, err := uuid.Parse(sid); err == nil {
				placeholders = append(placeholders, fmt.Sprintf("$%d", argIdx))
				args = append(args, sid)
				argIdx++
			}
		}
		if len(placeholders) > 0 {
			filterClause += fmt.Sprintf(` AND dp.id IN (
				SELECT package_id FROM server_dcp_inventory
				WHERE status = 'online' AND server_id IN (%s)
			)`, strings.Join(placeholders, ","))
		}
	}

	if search != "" {
		filterClause += fmt.Sprintf(` AND (LOWER(dp.package_name) LIKE $%d OR LOWER(dp.content_title) LIKE $%d)`, argIdx, argIdx+1)
		like := "%" + strings.ToLower(search) + "%"
		args = append(args, like, like)
		argIdx += 2
	}
	if contentKind != "" {
		filterClause += fmt.Sprintf(` AND LOWER(dp.content_kind) = $%d`, argIdx)
		args = append(args, strings.ToLower(contentKind))
		argIdx++
	}

	// Count total matching rows
	countQuery := `SELECT COUNT(DISTINCT dp.id) FROM dcp_packages dp WHERE ` + filterClause
	var totalCount int
	if err := s.database.QueryRow(countQuery, args...).Scan(&totalCount); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to count DCPs", err.Error())
		return
	}

	// Data query — paginated only when limit > 0
	dataQuery := fmt.Sprintf(`
		SELECT DISTINCT dp.id, dp.assetmap_uuid, dp.package_name, dp.content_title, dp.content_kind,
		       dp.issuer, dp.total_size_bytes, dp.file_count, dp.discovered_at
		FROM dcp_packages dp
		WHERE %s
		ORDER BY dp.discovered_at DESC`, filterClause)

	dataArgs := make([]interface{}, len(args))
	copy(dataArgs, args)
	if limit > 0 {
		dataQuery += fmt.Sprintf(" LIMIT $%d OFFSET $%d", argIdx, argIdx+1)
		dataArgs = append(dataArgs, limit, offset)
	}

	rows, err := s.database.Query(dataQuery, dataArgs...)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query DCPs", err.Error())
		return
	}
	defer rows.Close()

	var dcps []map[string]interface{}
	for rows.Next() {
		var id, assetMapUUID uuid.UUID
		var packageName, contentTitle, contentKind2, issuer string
		var totalSize int64
		var fileCount int
		var discoveredAt time.Time

		if err := rows.Scan(&id, &assetMapUUID, &packageName, &contentTitle, &contentKind2,
			&issuer, &totalSize, &fileCount, &discoveredAt); err != nil {
			log.Printf("Error scanning DCP row: %v", err)
			continue
		}

		dcps = append(dcps, map[string]interface{}{
			"id":               id,
			"assetmap_uuid":    assetMapUUID,
			"package_name":     packageName,
			"content_title":    contentTitle,
			"content_kind":     contentKind2,
			"issuer":           issuer,
			"total_size_bytes": totalSize,
			"file_count":       fileCount,
			"discovered_at":    discoveredAt,
		})
	}
	if dcps == nil {
		dcps = []map[string]interface{}{}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"dcps":        dcps,
		"count":       len(dcps),
		"total_count": totalCount,
		"offset":      offset,
		"limit":       limit,
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

// handleRegisterVersion adds a new version to the catalog (used by build-release.sh)
func (s *Server) handleRegisterVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed", "")
		return
	}
	var body struct {
		Version      string `json:"version"`
		BuildTime    string `json:"build_time"`
		Checksum     string `json:"checksum"`
		SizeBytes    int64  `json:"size_bytes"`
		DownloadURL  string `json:"download_url"`
		IsStable     *bool  `json:"is_stable"`
		ReleaseNotes string `json:"release_notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid JSON", err.Error())
		return
	}
	if body.Version == "" || body.Checksum == "" || body.DownloadURL == "" {
		respondError(w, http.StatusBadRequest, "Missing required fields", "version, checksum, download_url required")
		return
	}
	isStable := true
	if body.IsStable != nil {
		isStable = *body.IsStable
	}
	_, err := s.database.DB.Exec(`
		INSERT INTO software_versions (version, build_time, checksum, size_bytes, download_url, is_stable, release_notes)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		ON CONFLICT (version) DO UPDATE SET
			build_time = EXCLUDED.build_time,
			checksum = EXCLUDED.checksum,
			size_bytes = EXCLUDED.size_bytes,
			download_url = EXCLUDED.download_url,
			is_stable = EXCLUDED.is_stable,
			release_notes = EXCLUDED.release_notes`,
		body.Version, body.BuildTime, body.Checksum, body.SizeBytes, body.DownloadURL, isStable, body.ReleaseNotes)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to register version", err.Error())
		return
	}
	log.Printf("Registered version %s in catalog", body.Version)
	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "Version registered",
		"version": body.Version,
	})
}

// handleListVersions returns all available software versions
func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT version, build_time, checksum, size_bytes, download_url, is_stable, release_notes, created_at
		FROM software_versions
		ORDER BY created_at DESC`

	rows, err := s.database.Query(query)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query versions", err.Error())
		return
	}
	defer rows.Close()

	var versions []map[string]interface{}
	for rows.Next() {
		var version, checksum, downloadURL string
		var releaseNotes sql.NullString
		var sizeBytes int64
		var isStable bool
		var buildTime, createdAt time.Time

		if err := rows.Scan(&version, &buildTime, &checksum, &sizeBytes, &downloadURL, &isStable, &releaseNotes, &createdAt); err != nil {
			log.Printf("Error scanning version row: %v", err)
			continue
		}

		versionData := map[string]interface{}{
			"version":      version,
			"build_time":   buildTime,
			"checksum":     checksum,
			"size_bytes":   sizeBytes,
			"download_url": downloadURL,
			"is_stable":    isStable,
			"created_at":   createdAt,
		}

		if releaseNotes.Valid {
			versionData["release_notes"] = releaseNotes.String
		}

		versions = append(versions, versionData)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"versions": versions,
		"count":    len(versions),
	})
}

// handleGetLatestVersion returns the latest stable version
func (s *Server) handleGetLatestVersion(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT version, build_time, checksum, size_bytes, download_url, release_notes, created_at
		FROM software_versions
		WHERE is_stable = true
		ORDER BY created_at DESC
		LIMIT 1`

	var version, checksum, downloadURL string
	var releaseNotes sql.NullString
	var sizeBytes int64
	var buildTime, createdAt time.Time

	err := s.database.QueryRow(query).Scan(&version, &buildTime, &checksum, &sizeBytes, &downloadURL, &releaseNotes, &createdAt)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "No stable version found", "")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query latest version", err.Error())
		return
	}

	versionData := map[string]interface{}{
		"version":      version,
		"build_time":   buildTime,
		"checksum":     checksum,
		"size_bytes":   sizeBytes,
		"download_url": downloadURL,
		"is_stable":    true,
		"created_at":   createdAt,
	}

	if releaseNotes.Valid {
		versionData["release_notes"] = releaseNotes.String
	}

	respondJSON(w, http.StatusOK, versionData)
}

// handleTriggerUpgrade sets a target version for a server to upgrade to
func (s *Server) handleTriggerUpgrade(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var request struct {
		TargetVersion string `json:"target_version"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	if request.TargetVersion == "" {
		respondError(w, http.StatusBadRequest, "Missing target_version", "")
		return
	}

	// Verify version exists
	var exists bool
	err = s.database.QueryRow("SELECT EXISTS(SELECT 1 FROM software_versions WHERE version = $1)", request.TargetVersion).Scan(&exists)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}
	if !exists {
		respondError(w, http.StatusNotFound, "Version not found", "")
		return
	}

	log.Printf("Upgrade triggered for server %s to version %s", serverID, request.TargetVersion)

	// If this process is the target (main server upgrading itself), run upgrade locally then restart
	if s.selfServerID != nil && *s.selfServerID == serverID {
		// Set status in database first
		s.database.DB.Exec(`
			UPDATE servers
			SET target_version = $1, upgrade_status = 'pending', updated_at = CURRENT_TIMESTAMP
			WHERE id = $2`,
			request.TargetVersion, serverID)

		go func() {
			baseURL := "http://127.0.0.1:" + strconv.Itoa(s.port)
			if err := updater.PerformSelfUpgrade(baseURL, request.TargetVersion); err != nil {
				log.Printf("Self-upgrade failed: %v", err)
				s.database.DB.Exec(`UPDATE servers SET upgrade_status = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, "failed", serverID)
				return
			}
			s.database.DB.Exec(`UPDATE servers SET upgrade_status = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, "success", serverID)
			log.Printf("Self-upgrade complete; restarting in 2s")
			time.Sleep(2 * time.Second)
			syscall.Kill(os.Getpid(), syscall.SIGTERM)
		}()

		s.logActivity(r, "server.upgrade", "servers", "server", vars["id"], "", "", "success")
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message":        "Upgrade triggered successfully (local)",
			"server_id":      serverID,
			"target_version": request.TargetVersion,
			"status":         "pending",
		})
		return
	}

	// Try WebSocket first (instant delivery if client is connected)
	if s.wsHub != nil && s.wsHub.IsClientConnected(serverID) {
		payload := map[string]interface{}{
			"version": request.TargetVersion,
		}
		if err := s.wsHub.SendCommandToClient(serverID, ws.CommandUpgrade, payload); err == nil {
			log.Printf("Upgrade command sent via WebSocket to server %s", serverID)
			s.logActivity(r, "server.upgrade", "servers", "server", vars["id"], "", "", "success")
			respondJSON(w, http.StatusOK, map[string]interface{}{
				"message":        "Upgrade command sent successfully (via WebSocket)",
				"server_id":      serverID,
				"target_version": request.TargetVersion,
				"status":         "sent",
				"method":         "websocket",
			})
			return
		}
		log.Printf("WebSocket send failed for %s, falling back to database flag", serverID)
	}

	// Fallback to database flag for HTTP polling (legacy method)
	_, err = s.database.DB.Exec(`
		UPDATE servers
		SET target_version = $1, upgrade_status = 'pending', updated_at = CURRENT_TIMESTAMP
		WHERE id = $2`,
		request.TargetVersion, serverID)

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to set upgrade target", err.Error())
		return
	}

	s.logActivity(r, "server.upgrade", "servers", "server", vars["id"], "", "", "success")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":        "Upgrade triggered successfully (polling mode)",
		"server_id":      serverID,
		"target_version": request.TargetVersion,
		"status":         "pending",
		"method":         "polling",
	})
}

// handleRestartServer triggers a restart for a specific server
func (s *Server) handleRestartServer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	// If this process is the target (main server restarting itself), restart locally after a short delay
	if s.selfServerID != nil && *s.selfServerID == serverID {
		log.Printf("Restart target is this server; scheduling local restart in 2s")
		go func() {
			time.Sleep(2 * time.Second)
			log.Printf("Sending SIGTERM for self-restart (systemd will restart the service)")
			syscall.Kill(os.Getpid(), syscall.SIGTERM)
		}()

		s.logActivity(r, "server.restart", "servers", "server", vars["id"], "", "", "success")
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"message":   "Restart triggered successfully (local)",
			"server_id": serverID,
			"status":    "pending",
		})
		return
	}

	// Try WebSocket first (instant delivery if client is connected)
	if s.wsHub != nil && s.wsHub.IsClientConnected(serverID) {
		if err := s.wsHub.SendCommandToClient(serverID, ws.CommandRestart, nil); err == nil {
			log.Printf("Restart command sent via WebSocket to server %s", serverID)
			s.logActivity(r, "server.restart", "servers", "server", vars["id"], "", "", "success")
			respondJSON(w, http.StatusOK, map[string]interface{}{
				"message":   "Restart command sent successfully (via WebSocket)",
				"server_id": serverID,
				"status":    "sent",
				"method":    "websocket",
			})
			return
		}
		log.Printf("WebSocket send failed for %s, falling back to database flag", serverID)
	}

	// Fallback to database flag for HTTP polling (legacy method)
	// Set a restart flag by setting target_version to "restart"
	// The update agent (on remote clients) will detect this and restart their service
	_, err = s.database.DB.Exec(`
		UPDATE servers
		SET target_version = 'restart', upgrade_status = 'pending', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1`,
		serverID)

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to trigger restart", err.Error())
		return
	}

	log.Printf("Restart flag set in database for server %s (will be polled)", serverID)

	s.logActivity(r, "server.restart", "servers", "server", vars["id"], "", "", "success")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":   "Restart triggered successfully (polling mode)",
		"server_id": serverID,
		"status":    "pending",
		"method":    "polling",
	})
}

// handlePendingAction returns the pending restart/upgrade action for the calling server (used by update agent)
func (s *Server) handlePendingAction(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var targetVersion sql.NullString
	var upgradeStatus sql.NullString
	err = s.database.QueryRow(`
		SELECT target_version, upgrade_status FROM servers WHERE id = $1`,
		serverID).Scan(&targetVersion, &upgradeStatus)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Server not found", "")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}

	status := upgradeStatus.String
	if status != "pending" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"action": nil})
		return
	}

	target := targetVersion.String
	if target == "restart" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"action": "restart"})
		return
	}
	if target != "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"action":         "upgrade",
			"target_version": target,
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"action": nil})
}

// handlePendingTransfers returns transfers that need to be downloaded by a specific server.
// Returns both 'queued' transfers AND 'downloading' transfers with 0 progress (stuck from
// a previous failed attempt) so the client can re-initiate them.
func (s *Server) handlePendingTransfers(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	log.Printf("[pending-transfers] Request from server %s", serverID)

	// Return transfers that are:
	// 1. 'queued' - waiting to be started
	// 2. 'downloading' - in progress (may need resuming after client restart)
	// 3. 'active' - alternative status for in-progress transfers
	// The client's TransferProcessor will skip transfers it's already handling.
	// PostgreSQL piece completion ensures already-downloaded pieces aren't re-downloaded.
	rows, err := s.database.Query(`
		SELECT
			t.id,
			t.torrent_id,
			t.source_server_id,
			t.destination_server_id,
			t.priority,
			t.status,
			t.progress_percent,
			COALESCE(dp.id::text, '') as package_id,
			COALESCE(dp.package_name, '') as package_name,
			COALESCE(dp.total_size_bytes, 0) as total_size_bytes,
			COALESCE(dt.info_hash, '') as info_hash
		FROM transfers t
		LEFT JOIN dcp_torrents dt ON t.torrent_id = dt.id
		LEFT JOIN dcp_packages dp ON dt.package_id = dp.id
		WHERE t.destination_server_id = $1
		  AND t.status IN ('queued', 'downloading', 'checking', 'active')
		ORDER BY t.priority DESC, t.created_at ASC
		LIMIT 10
	`, serverID)
	if err != nil {
		log.Printf("[pending-transfers] Database error for server %s: %v", serverID, err)
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}
	defer rows.Close()

	transfers := []map[string]interface{}{}
	for rows.Next() {
		var id, torrentID, destServerID uuid.UUID
		var sourceServerID sql.NullString
		var priority int
		var status string
		var progressPercent float64
		var packageID, packageName string
		var totalSizeBytes int64
		var infoHash string

		if err := rows.Scan(&id, &torrentID, &sourceServerID, &destServerID, &priority,
			&status, &progressPercent,
			&packageID, &packageName, &totalSizeBytes, &infoHash); err != nil {
			log.Printf("[pending-transfers] Error scanning transfer row: %v", err)
			continue
		}

		log.Printf("[pending-transfers]   Found: id=%s package=%q status=%s progress=%.1f%% info_hash=%s",
			id, packageName, status, progressPercent, infoHash)

		transfer := map[string]interface{}{
			"id":                    id,
			"torrent_id":            torrentID,
			"package_id":            packageID,
			"package_name":          packageName,
			"destination_server_id": destServerID,
			"priority":              priority,
			"total_size_bytes":      totalSizeBytes,
			"torrent_file_url":      fmt.Sprintf("/api/v1/torrents/%s/file", infoHash),
		}
		if sourceServerID.Valid {
			transfer["source_server_id"] = sourceServerID.String
		}

		transfers = append(transfers, transfer)
	}

	log.Printf("[pending-transfers] Returning %d transfer(s) for server %s", len(transfers), serverID)
	respondJSON(w, http.StatusOK, transfers)
}

// handleTransferCommands returns pending commands (pause, resume, cancel) for a server's transfers.
// The client polls this to discover commands it needs to execute.
func (s *Server) handleTransferCommands(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	rows, err := s.database.Query(`
		SELECT t.id, COALESCE(dt.info_hash, '') as info_hash,
		       COALESCE(dp.package_name, '') as package_name,
		       COALESCE(t.pending_command, '') as command,
		       COALESCE(t.delete_data, false) as delete_data,
		       t.status
		FROM transfers t
		LEFT JOIN dcp_torrents dt ON t.torrent_id = dt.id
		LEFT JOIN dcp_packages dp ON dt.package_id = dp.id
		WHERE t.destination_server_id = $1
		  AND COALESCE(t.pending_command, '') != ''
		  AND COALESCE(t.command_acknowledged, true) = false
	`, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]interface{}
	for rows.Next() {
		var id, infoHash, packageName, command, status string
		var deleteData bool
		if err := rows.Scan(&id, &infoHash, &packageName, &command, &deleteData, &status); err != nil {
			continue
		}
		items = append(items, map[string]interface{}{
			"id":           id,
			"info_hash":    infoHash,
			"package_name": packageName,
			"command":      command,
			"delete_data":  deleteData,
			"status":       status,
		})
	}
	if items == nil {
		items = []map[string]interface{}{}
	}
	respondJSON(w, http.StatusOK, items)
}

// handleTransferCommandAck marks a command as acknowledged by the client
func (s *Server) handleTransferCommandAck(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var req struct {
		TransferID string `json:"transfer_id"`
		Result     string `json:"result"` // "done", "deleted", "kept", "error"
		Message    string `json:"message,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	_, err = s.database.Exec(`
		UPDATE transfers SET command_acknowledged = true, pending_command = '', updated_at = $1
		WHERE id = $2 AND destination_server_id = $3
	`, time.Now(), req.TransferID, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}

	log.Printf("[command-ack] Server %s acknowledged command for transfer %s: %s (%s)",
		serverID, req.TransferID, req.Result, req.Message)
	respondJSON(w, http.StatusOK, map[string]string{"message": "acknowledged"})
}

// handleContentCommands returns pending content commands for a server (client polls this)
func (s *Server) handleContentCommands(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	rows, err := s.db.Query(`
		SELECT id, package_id, package_name, info_hash, command, COALESCE(target_path, '')
		FROM content_commands
		WHERE server_id = $1 AND status = 'pending'
		ORDER BY created_at ASC
	`, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}
	defer rows.Close()

	var items []map[string]interface{}
	for rows.Next() {
		var id, packageID, packageName, infoHash, command, targetPath string
		if err := rows.Scan(&id, &packageID, &packageName, &infoHash, &command, &targetPath); err != nil {
			continue
		}
		items = append(items, map[string]interface{}{
			"id":           id,
			"package_id":   packageID,
			"package_name": packageName,
			"info_hash":    infoHash,
			"command":      command,
			"target_path":  targetPath,
		})
	}
	if items == nil {
		items = []map[string]interface{}{}
	}
	respondJSON(w, http.StatusOK, items)
}

// handleContentCommandAck acknowledges a content command from the client
func (s *Server) handleContentCommandAck(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var req struct {
		CommandID string `json:"command_id"`
		Result    string `json:"result"`  // "deleted", "error"
		Message   string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	now := time.Now()
	status := "completed"
	if req.Result == "error" {
		status = "error"
	}

	_, err = s.db.Exec(`
		UPDATE content_commands SET status = $1, result_message = $2, acknowledged_at = $3
		WHERE id = $4 AND server_id = $5
	`, status, req.Message, now, req.CommandID, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}

	// If successfully deleted, remove from server_dcp_inventory
	if req.Result == "deleted" {
		var packageID string
		err := s.db.QueryRow("SELECT package_id FROM content_commands WHERE id = $1", req.CommandID).Scan(&packageID)
		if err == nil {
			_, err = s.db.Exec("DELETE FROM server_dcp_inventory WHERE server_id = $1 AND package_id = $2", serverID, packageID)
			if err != nil {
				log.Printf("[content-cmd-ack] Warning: failed to remove inventory for server=%s package=%s: %v", serverID, packageID, err)
			} else {
				log.Printf("[content-cmd-ack] Removed inventory entry for server=%s package=%s", serverID, packageID)
			}
		}
	}

	log.Printf("[content-cmd-ack] Server %s acknowledged content command %s: %s (%s)", serverID, req.CommandID, req.Result, req.Message)
	respondJSON(w, http.StatusOK, map[string]string{"message": "acknowledged"})
}

// handleActionDone clears the pending action after the agent has completed restart or upgrade
func (s *Server) handleActionDone(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var request struct {
		Action string `json:"action"` // "restart" or "upgrade"
		Status string `json:"status"` // "success" or "failed"
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}
	if request.Action != "restart" && request.Action != "upgrade" {
		respondError(w, http.StatusBadRequest, "action must be restart or upgrade", "")
		return
	}
	if request.Status == "" {
		request.Status = "success"
	}

	_, err = s.database.DB.Exec(`
		UPDATE servers SET target_version = NULL, upgrade_status = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`,
		request.Status, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to clear action", err.Error())
		return
	}

	log.Printf("Action %s completed for server %s (status: %s)", request.Action, serverID, request.Status)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Action acknowledged",
	})
}

// handleScanTrigger starts a full library scan on this server (called by Rescan UI or by main server proxying to this site)
func (s *Server) handleScanTrigger(w http.ResponseWriter, r *http.Request) {
	if s.triggerScan == nil {
		respondError(w, http.StatusNotImplemented, "Scan trigger not configured", "")
		return
	}
	go s.triggerScan()
	respondJSON(w, http.StatusAccepted, map[string]string{"message": "Scan started"})
}

// handleScanStatus returns the latest scan status for this server (used by main server when polling a site)
func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	if s.selfServerID == nil {
		respondError(w, http.StatusInternalServerError, "Server ID not set", "")
		return
	}
	logEntry, err := s.database.GetLatestScanLog(*s.selfServerID)
	if err != nil {
		if err == sql.ErrNoRows {
			respondJSON(w, http.StatusOK, map[string]interface{}{
				"status": "idle", "message": "No scan has run yet",
			})
			return
		}
		respondError(w, http.StatusInternalServerError, "Failed to get scan status", err.Error())
		return
	}
	var completedAt interface{}
	if logEntry.CompletedAt != nil {
		completedAt = logEntry.CompletedAt
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"id":                logEntry.ID,
		"server_id":         logEntry.ServerID,
		"scan_type":         logEntry.ScanType,
		"status":            logEntry.Status,
		"started_at":        logEntry.StartedAt,
		"completed_at":     completedAt,
		"packages_found":    logEntry.PackagesFound,
		"packages_added":    logEntry.PackagesAdded,
		"packages_updated":  logEntry.PackagesUpdated,
		"packages_removed": logEntry.PackagesRemoved,
		"errors":           logEntry.Errors,
	})
}

// handleRescanServer triggers a full library rescan on a server (this server or a remote site)
func (s *Server) handleRescanServer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}
	// If targeting this server, trigger locally
	if s.selfServerID != nil && serverID == *s.selfServerID {
		if s.triggerScan == nil {
			respondError(w, http.StatusNotImplemented, "Scan trigger not configured", "")
			return
		}
		go s.triggerScan()
		s.logActivity(r, "server.rescan", "servers", "server", vars["id"], "", "", "success")
		respondJSON(w, http.StatusAccepted, map[string]string{"message": "Rescan started"})
		return
	}
	// Otherwise call the remote server's API
	server, err := s.database.GetServer(serverID)
	if err != nil || server == nil {
		respondError(w, http.StatusNotFound, "Server not found", "")
		return
	}
	if server.APIURL == "" {
		respondError(w, http.StatusBadRequest, "Server has no API URL", "")
		return
	}
	url := strings.TrimSuffix(server.APIURL, "/") + "/api/v1/scan/trigger"
	req, err := http.NewRequestWithContext(r.Context(), "POST", url, nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create request", err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		respondError(w, http.StatusBadGateway, "Failed to reach server", err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		respondError(w, resp.StatusCode, "Server returned error", string(body))
		return
	}
	s.logActivity(r, "server.rescan", "servers", "server", vars["id"], "", "", "success")
	respondJSON(w, http.StatusAccepted, map[string]string{"message": "Rescan started on remote server"})
}

// handleServerScanStatus returns the current scan status for a server (this server or remote)
func (s *Server) handleServerScanStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}
	if s.selfServerID != nil && serverID == *s.selfServerID {
		s.handleScanStatus(w, r)
		return
	}
	server, err := s.database.GetServer(serverID)
	if err != nil || server == nil {
		respondError(w, http.StatusNotFound, "Server not found", "")
		return
	}
	if server.APIURL == "" {
		respondError(w, http.StatusBadRequest, "Server has no API URL", "")
		return
	}
	url := strings.TrimSuffix(server.APIURL, "/") + "/api/v1/scan/status"
	req, err := http.NewRequestWithContext(r.Context(), "GET", url, nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create request", err.Error())
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		respondError(w, http.StatusBadGateway, "Failed to reach server", err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to read response", err.Error())
		return
	}
	if resp.StatusCode != http.StatusOK {
		respondError(w, resp.StatusCode, "Server returned error", string(body))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
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

// handleCreateBuild triggers a new build AND deployment via deployTests.sh
func (s *Server) handleCreateBuild(w http.ResponseWriter, r *http.Request) {
	var body struct {
		VersionName string `json:"version_name"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	log.Printf("Build & Deploy triggered via API (version: %s)", body.VersionName)

	// Use deployTests.sh which builds, deploys, and restarts services
	scriptPath := "/home/appbox/DCPCLOUDAPP/omnicloud/deployTests.sh"
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		respondError(w, http.StatusInternalServerError, "Deploy script not found", scriptPath)
		return
	}

	go func() {
		cmd := exec.Command("/bin/bash", scriptPath)
		cmd.Dir = "/home/appbox/DCPCLOUDAPP/omnicloud"
		if body.VersionName != "" {
			cmd.Env = append(os.Environ(), fmt.Sprintf("VERSION=%s", body.VersionName))
		}
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("Build & Deploy failed: %v\nOutput: %s", err, string(output))
		} else {
			log.Printf("Build & Deploy completed:\n%s", string(output))
		}

		// After successful deployment, also create release tarball for distribution
		releasePath := "/home/appbox/DCPCLOUDAPP/omnicloud/scripts/build-release.sh"
		if _, err := os.Stat(releasePath); err == nil {
			releaseCmd := exec.Command("/bin/bash", releasePath)
			releaseCmd.Dir = "/home/appbox/DCPCLOUDAPP/omnicloud"
			if body.VersionName != "" {
				releaseCmd.Env = append(os.Environ(), fmt.Sprintf("VERSION=%s", body.VersionName))
			}
			releaseOutput, releaseErr := releaseCmd.CombinedOutput()
			if releaseErr != nil {
				log.Printf("Release package creation failed: %v\nOutput: %s", releaseErr, string(releaseOutput))
			} else {
				log.Printf("Release package created:\n%s", string(releaseOutput))
			}
		}
	}()

	respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"message": "Build, deployment, and release package creation started in background",
	})
}

// handleLogIngest receives logs from client servers
func (s *Server) handleLogIngest(w http.ResponseWriter, r *http.Request) {
	var logs struct {
		ServerID string   `json:"server_id"`
		Lines    []string `json:"lines"`
	}
	if err := json.NewDecoder(r.Body).Decode(&logs); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", "")
		return
	}
	
	for _, line := range logs.Lines {
		log.Printf("[%s] %s", logs.ServerID, line)
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleInstallScript serves the quick-install script for one-command client installation.
// Tries multiple paths so it works from dev (DCPCLOUDAPP) and production (/opt/omnicloud).
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	// Try paths in order: working dir, executable-relative, then dev path
	var scriptPath string
	candidates := []string{}

	if workDir, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(workDir, "scripts", "quick-install.sh"))
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(exeDir, "scripts", "quick-install.sh"))
		// If binary is in bin/, script is often in sibling scripts/
		candidates = append(candidates, filepath.Join(filepath.Dir(exeDir), "scripts", "quick-install.sh"))
	}
	candidates = append(candidates, "/home/appbox/DCPCLOUDAPP/omnicloud/scripts/quick-install.sh")

	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			scriptPath = p
			break
		}
	}
	if scriptPath == "" {
		log.Printf("Install script not found; tried: %v", candidates)
		http.Error(w, "Install script not found", http.StatusNotFound)
		return
	}

	content, err := ioutil.ReadFile(scriptPath)
	if err != nil {
		log.Printf("Error reading install script at %s: %v", scriptPath, err)
		http.Error(w, "Install script not found", http.StatusNotFound)
		return
	}

	// Serve as plain text shell script
	w.Header().Set("Content-Type", "text/x-shellscript")
	w.Header().Set("Content-Disposition", "inline; filename=\"install-omnicloud.sh\"")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	w.Write(content)

	log.Printf("Install script served to %s", r.RemoteAddr)
}

// handleAdminDBReset clears all content, hashing progress, and torrents from the DB (keeps servers).
func (s *Server) handleAdminDBReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondError(w, http.StatusMethodNotAllowed, "Method not allowed", "")
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("Admin DB reset: begin tx: %v", err)
		respondError(w, http.StatusInternalServerError, "Database error", "")
		return
	}
	defer tx.Rollback()

	// Optional tables (may not exist on all migrations) — use DO block so missing tables don't abort tx
	_, err = tx.Exec(`
		DO $$
		BEGIN
			IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'torrent_announce_attempts') THEN
				TRUNCATE TABLE torrent_announce_attempts RESTART IDENTITY CASCADE;
			END IF;
			IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'torrent_generation_claim') THEN
				TRUNCATE TABLE torrent_generation_claim CASCADE;
			END IF;
			IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'server_torrent_status') THEN
				TRUNCATE TABLE server_torrent_status CASCADE;
			END IF;
			IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'server_torrent_stats') THEN
				TRUNCATE TABLE server_torrent_stats CASCADE;
			END IF;
			IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'torrent_piece_completion') THEN
				TRUNCATE TABLE torrent_piece_completion CASCADE;
			END IF;
		END $$;
	`)
	if err != nil {
		log.Printf("Admin DB reset: optional tables: %v", err)
		respondError(w, http.StatusInternalServerError, "Database error during reset", "")
		return
	}

	// Required tables in dependency order
	truncates := []struct {
		table string
		id    bool
	}{
		{"torrent_seeders", false}, {"transfers", false}, {"torrent_queue", false}, {"dcp_torrents", false},
		{"server_dcp_inventory", false}, {"scan_logs", true},
		{"dcp_reels", false}, {"dcp_compositions", false}, {"dcp_assets", false}, {"dcp_packing_lists", false}, {"dcp_packages", false},
	}
	for _, t := range truncates {
		stmt := "TRUNCATE TABLE " + t.table + " CASCADE"
		if t.id {
			stmt = "TRUNCATE TABLE " + t.table + " RESTART IDENTITY CASCADE"
		}
		if _, err := tx.Exec(stmt); err != nil {
			log.Printf("Admin DB reset: truncate %s: %v", t.table, err)
			respondError(w, http.StatusInternalServerError, "Database error during reset", "")
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("Admin DB reset: commit: %v", err)
		respondError(w, http.StatusInternalServerError, "Database error", "")
		return
	}

	log.Printf("Admin DB reset completed (content, hashing, torrents cleared; servers kept)")
	s.logActivity(r, "system.db_reset", "system", "", "", "", "", "success")
	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Database reset complete. All content, hashing progress, and torrents cleared. Servers kept.",
	})
}
