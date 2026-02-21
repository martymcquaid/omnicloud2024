package api

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// ServerSettings represents the configuration settings for a server
type ServerSettings struct {
	ServerID                    string                   `json:"server_id"`
	DisplayName                 string                   `json:"display_name"`
	DownloadLocation            string                   `json:"download_location"`
	TorrentDownloadLocation     string                   `json:"torrent_download_location"`
	WatchFolder                 string                   `json:"watch_folder"`
	AutoCleanupAfterIngestion   bool                     `json:"auto_cleanup_after_ingestion"`
	LibraryLocations            []ServerLibraryLocation  `json:"library_locations"`
}

// ServerLibraryLocation represents a library path for a server
type ServerLibraryLocation struct {
	ID           string    `json:"id"`
	ServerID     string    `json:"server_id"`
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	IsActive     bool      `json:"is_active"`
	LocationType string    `json:"location_type"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// handleGetServerSettings returns the settings for a specific server
func (s *Server) handleGetServerSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	// Get server basic settings
	var displayName, downloadLocation, torrentDownloadLocation, watchFolder sql.NullString
	var autoCleanup bool
	err = s.database.QueryRow(`
		SELECT
			COALESCE(display_name, ''),
			COALESCE(download_location, ''),
			COALESCE(torrent_download_location, ''),
			COALESCE(watch_folder, ''),
			COALESCE(auto_cleanup_after_ingestion, false)
		FROM servers
		WHERE id = $1
	`, serverID).Scan(&displayName, &downloadLocation, &torrentDownloadLocation, &watchFolder, &autoCleanup)

	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Server not found", "")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}

	// Get library locations
	rows, err := s.database.Query(`
		SELECT id, server_id, name, path, is_active, COALESCE(location_type, 'standard'), created_at, updated_at
		FROM server_library_locations
		WHERE server_id = $1
		ORDER BY created_at ASC
	`, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}
	defer rows.Close()

	var libraryLocations []ServerLibraryLocation
	for rows.Next() {
		var loc ServerLibraryLocation
		var id, sid uuid.UUID
		err := rows.Scan(&id, &sid, &loc.Name, &loc.Path, &loc.IsActive, &loc.LocationType, &loc.CreatedAt, &loc.UpdatedAt)
		if err != nil {
			log.Printf("Error scanning library location: %v", err)
			continue
		}
		loc.ID = id.String()
		loc.ServerID = sid.String()
		libraryLocations = append(libraryLocations, loc)
	}

	settings := ServerSettings{
		ServerID:                    serverID.String(),
		DisplayName:                 displayName.String,
		DownloadLocation:            downloadLocation.String,
		TorrentDownloadLocation:     torrentDownloadLocation.String,
		WatchFolder:                 watchFolder.String,
		AutoCleanupAfterIngestion:   autoCleanup,
		LibraryLocations:            libraryLocations,
	}

	respondJSON(w, http.StatusOK, settings)
}

// handleUpdateServerSettings updates the settings for a specific server
func (s *Server) handleUpdateServerSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var settings struct {
		DisplayName               string `json:"display_name"`
		DownloadLocation          string `json:"download_location"`
		TorrentDownloadLocation   string `json:"torrent_download_location"`
		WatchFolder               string `json:"watch_folder"`
		AutoCleanupAfterIngestion *bool  `json:"auto_cleanup_after_ingestion,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	// Update server settings (display_name is user-defined and not overwritten by client sync)
	autoCleanup := false
	if settings.AutoCleanupAfterIngestion != nil {
		autoCleanup = *settings.AutoCleanupAfterIngestion
	}
	_, err = s.database.Exec(`
		UPDATE servers
		SET display_name = NULLIF(TRIM($1), ''),
		    download_location = $2,
		    torrent_download_location = $3,
		    watch_folder = $4,
		    auto_cleanup_after_ingestion = $5,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $6
	`, settings.DisplayName, settings.DownloadLocation, settings.TorrentDownloadLocation, settings.WatchFolder, autoCleanup, serverID)

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update settings", err.Error())
		return
	}

	log.Printf("Updated settings for server %s", serverID)

	s.logActivity(r, "settings.update", "system", "server", vars["id"], "", "", "success")

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Settings updated successfully",
	})
}

// handleAddLibraryLocation adds a new library location for a server
func (s *Server) handleAddLibraryLocation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var location struct {
		Name         string `json:"name"`
		Path         string `json:"path"`
		LocationType string `json:"location_type"`
	}

	if err := json.NewDecoder(r.Body).Decode(&location); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	if location.Name == "" || location.Path == "" {
		respondError(w, http.StatusBadRequest, "Name and path are required", "")
		return
	}

	if location.LocationType == "" {
		location.LocationType = "standard"
	}

	// If setting as rosettabridge, clear any existing rosettabridge location for this server
	if location.LocationType == "rosettabridge" {
		s.database.Exec(`
			UPDATE server_library_locations SET location_type = 'standard', updated_at = CURRENT_TIMESTAMP
			WHERE server_id = $1 AND location_type = 'rosettabridge'
		`, serverID)
	}

	// Insert new library location
	id := uuid.New()
	_, err = s.database.Exec(`
		INSERT INTO server_library_locations (id, server_id, name, path, is_active, location_type)
		VALUES ($1, $2, $3, $4, true, $5)
	`, id, serverID, location.Name, location.Path, location.LocationType)

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to add library location", err.Error())
		return
	}

	log.Printf("Added library location '%s' for server %s", location.Name, serverID)

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "Library location added successfully",
		"id":      id.String(),
	})
}

// handleUpdateLibraryLocation updates an existing library location
func (s *Server) handleUpdateLibraryLocation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	locationID, err := uuid.Parse(vars["location_id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid location ID", err.Error())
		return
	}

	var location struct {
		Name         string `json:"name"`
		Path         string `json:"path"`
		IsActive     bool   `json:"is_active"`
		LocationType string `json:"location_type"`
	}

	if err := json.NewDecoder(r.Body).Decode(&location); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	if location.LocationType == "" {
		location.LocationType = "standard"
	}

	// If setting as rosettabridge, clear any existing rosettabridge location for this server
	if location.LocationType == "rosettabridge" {
		// Get the server_id for this location
		var serverIDForLoc uuid.UUID
		s.database.QueryRow(`SELECT server_id FROM server_library_locations WHERE id = $1`, locationID).Scan(&serverIDForLoc)
		if serverIDForLoc != uuid.Nil {
			s.database.Exec(`
				UPDATE server_library_locations SET location_type = 'standard', updated_at = CURRENT_TIMESTAMP
				WHERE server_id = $1 AND location_type = 'rosettabridge' AND id != $2
			`, serverIDForLoc, locationID)
		}
	}

	// Update library location
	result, err := s.database.Exec(`
		UPDATE server_library_locations
		SET name = $1,
		    path = $2,
		    is_active = $3,
		    location_type = $4,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $5
	`, location.Name, location.Path, location.IsActive, location.LocationType, locationID)

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update library location", err.Error())
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondError(w, http.StatusNotFound, "Library location not found", "")
		return
	}

	log.Printf("Updated library location %s", locationID)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Library location updated successfully",
	})
}

// handleDeleteLibraryLocation deletes a library location
func (s *Server) handleDeleteLibraryLocation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	locationID, err := uuid.Parse(vars["location_id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid location ID", err.Error())
		return
	}

	// Delete library location
	result, err := s.database.Exec(`
		DELETE FROM server_library_locations
		WHERE id = $1
	`, locationID)

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to delete library location", err.Error())
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondError(w, http.StatusNotFound, "Library location not found", "")
		return
	}

	log.Printf("Deleted library location %s", locationID)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Library location deleted successfully",
	})
}

// IngestionStatusRecord represents a DCP ingestion tracking record
type IngestionStatusRecord struct {
	ID               string     `json:"id"`
	ServerID         string     `json:"server_id"`
	PackageID        string     `json:"package_id"`
	InfoHash         string     `json:"info_hash"`
	DownloadPath     string     `json:"download_path"`
	RosettaBridgePath string    `json:"rosettabridge_path"`
	Status           string     `json:"status"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
	SwitchedAt       *time.Time `json:"switched_at,omitempty"`
	CleanedAt        *time.Time `json:"cleaned_at,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// handleGetIngestionStatus returns ingestion status records for a server
func (s *Server) handleGetIngestionStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	rows, err := s.database.Query(`
		SELECT id, server_id, package_id, info_hash, download_path,
		       COALESCE(rosettabridge_path, ''), status,
		       verified_at, switched_at, cleaned_at,
		       COALESCE(error_message, ''), created_at, updated_at
		FROM dcp_ingestion_status
		WHERE server_id = $1
		ORDER BY created_at DESC
	`, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database error", err.Error())
		return
	}
	defer rows.Close()

	var records []IngestionStatusRecord
	for rows.Next() {
		var rec IngestionStatusRecord
		var id, sid, pid uuid.UUID
		err := rows.Scan(&id, &sid, &pid, &rec.InfoHash, &rec.DownloadPath,
			&rec.RosettaBridgePath, &rec.Status,
			&rec.VerifiedAt, &rec.SwitchedAt, &rec.CleanedAt,
			&rec.ErrorMessage, &rec.CreatedAt, &rec.UpdatedAt)
		if err != nil {
			log.Printf("Error scanning ingestion record: %v", err)
			continue
		}
		rec.ID = id.String()
		rec.ServerID = sid.String()
		rec.PackageID = pid.String()
		records = append(records, rec)
	}

	respondJSON(w, http.StatusOK, records)
}

// handleReportIngestion receives ingestion status updates from client servers
func (s *Server) handleReportIngestion(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var report struct {
		PackageID        string `json:"package_id"`
		InfoHash         string `json:"info_hash"`
		DownloadPath     string `json:"download_path"`
		RosettaBridgePath string `json:"rosettabridge_path"`
		Status           string `json:"status"`
		ErrorMessage     string `json:"error_message"`
	}

	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err.Error())
		return
	}

	packageID, err := uuid.Parse(report.PackageID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid package ID", err.Error())
		return
	}

	// Upsert ingestion status
	now := time.Now()
	var verifiedAt, switchedAt, cleanedAt *time.Time
	switch report.Status {
	case "verified":
		verifiedAt = &now
	case "seeding_switched":
		switchedAt = &now
	case "cleanup_done":
		cleanedAt = &now
	}

	_, err = s.database.Exec(`
		INSERT INTO dcp_ingestion_status (id, server_id, package_id, info_hash, download_path, rosettabridge_path, status, verified_at, switched_at, cleaned_at, error_message)
		VALUES (uuid_generate_v4(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (server_id, package_id) DO UPDATE SET
			info_hash = EXCLUDED.info_hash,
			rosettabridge_path = EXCLUDED.rosettabridge_path,
			status = EXCLUDED.status,
			verified_at = COALESCE(EXCLUDED.verified_at, dcp_ingestion_status.verified_at),
			switched_at = COALESCE(EXCLUDED.switched_at, dcp_ingestion_status.switched_at),
			cleaned_at = COALESCE(EXCLUDED.cleaned_at, dcp_ingestion_status.cleaned_at),
			error_message = EXCLUDED.error_message,
			updated_at = CURRENT_TIMESTAMP
	`, serverID, packageID, report.InfoHash, report.DownloadPath, report.RosettaBridgePath,
		report.Status, verifiedAt, switchedAt, cleanedAt, report.ErrorMessage)

	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update ingestion status", err.Error())
		return
	}

	log.Printf("[ingestion] Server %s reported ingestion status '%s' for package %s", serverID, report.Status, packageID)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Ingestion status updated",
	})
}
