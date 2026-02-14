package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// TorrentInfo represents torrent metadata
type TorrentInfo struct {
	ID               string    `json:"id"`
	PackageID        string    `json:"package_id"`
	InfoHash         string    `json:"info_hash"`
	PieceSize        int       `json:"piece_size"`
	TotalPieces      int       `json:"total_pieces"`
	FileCount        int       `json:"file_count"`
	TotalSizeBytes   int64     `json:"total_size_bytes"`
	CreatedByServerID string   `json:"created_by_server_id"`
	CreatedAt        time.Time `json:"created_at"`
}

// SeederInfo represents a seeder for a torrent
type SeederInfo struct {
	ID           string    `json:"id"`
	TorrentID    string    `json:"torrent_id"`
	ServerID     string    `json:"server_id"`
	ServerName   string    `json:"server_name,omitempty"`
	LocalPath    string    `json:"local_path"`
	Status       string    `json:"status"`
	UploadedBytes int64    `json:"uploaded_bytes"`
	LastAnnounce time.Time `json:"last_announce"`
}

// TransferInfo represents a DCP transfer
type TransferInfo struct {
	ID                   string    `json:"id"`
	TorrentID            string    `json:"torrent_id"`
	PackageID            string    `json:"package_id,omitempty"`
	PackageName          string    `json:"package_name,omitempty"`
	SourceServerID       *string   `json:"source_server_id"`
	DestinationServerID  string    `json:"destination_server_id"`
	RequestedBy          string    `json:"requested_by"`
	Status               string    `json:"status"`
	Priority             int       `json:"priority"`
	ProgressPercent      float64   `json:"progress_percent"`
	DownloadedBytes      int64     `json:"downloaded_bytes"`
	DownloadSpeedBps     int64     `json:"download_speed_bps"`
	UploadSpeedBps       int64     `json:"upload_speed_bps"`
	PeersConnected       int       `json:"peers_connected"`
	ETASeconds           *int      `json:"eta_seconds"`
	ErrorMessage         *string   `json:"error_message"`
	StartedAt            *time.Time `json:"started_at"`
	CompletedAt          *time.Time `json:"completed_at"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// CreateTransferRequest represents a request to create a transfer
type CreateTransferRequest struct {
	TorrentID           string  `json:"torrent_id"`
	DestinationServerID string  `json:"destination_server_id"`
	RequestedBy         string  `json:"requested_by"`
	Priority            *int    `json:"priority"`
}

// UpdateTransferRequest represents a request to update transfer progress
type UpdateTransferRequest struct {
	Status           *string  `json:"status"`
	ProgressPercent  *float64 `json:"progress_percent"`
	DownloadedBytes  *int64   `json:"downloaded_bytes"`
	DownloadSpeedBps *int64   `json:"download_speed_bps"`
	UploadSpeedBps   *int64   `json:"upload_speed_bps"`
	PeersConnected   *int     `json:"peers_connected"`
	ETASeconds       *int     `json:"eta_seconds"`
	ErrorMessage     *string  `json:"error_message"`
}

// handleListTorrents returns all registered torrents
func (s *Server) handleListTorrents(w http.ResponseWriter, r *http.Request) {
	// Optional query params for filtering
	packageID := r.URL.Query().Get("package_id")
	assetmapUUID := r.URL.Query().Get("assetmap_uuid")

	query := `
		SELECT t.id, t.package_id, t.info_hash, t.piece_size, t.total_pieces, 
		       t.file_count, t.total_size_bytes, t.created_by_server_id, t.created_at
		FROM dcp_torrents t
		WHERE 1=1
	`
	args := []interface{}{}

	if packageID != "" {
		query += " AND t.package_id = $1"
		args = append(args, packageID)
	} else if assetmapUUID != "" {
		// Look up package by assetmap UUID
		query += " AND t.package_id IN (SELECT id FROM dcp_packages WHERE assetmap_uuid = $1)"
		args = append(args, assetmapUUID)
	}

	query += " ORDER BY t.created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query torrents", "")
		return
	}
	defer rows.Close()

	var torrents []TorrentInfo
	for rows.Next() {
		var t TorrentInfo
		err := rows.Scan(&t.ID, &t.PackageID, &t.InfoHash, &t.PieceSize, &t.TotalPieces,
			&t.FileCount, &t.TotalSizeBytes, &t.CreatedByServerID, &t.CreatedAt)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to scan torrent", "")
			return
		}
		torrents = append(torrents, t)
	}

	respondJSON(w, http.StatusOK, torrents)
}

// handleGetTorrent returns a specific torrent by info hash
func (s *Server) handleGetTorrent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	infoHash := vars["info_hash"]

	var t TorrentInfo
	query := `
		SELECT id, package_id, info_hash, piece_size, total_pieces, 
		       file_count, total_size_bytes, created_by_server_id, created_at
		FROM dcp_torrents
		WHERE info_hash = $1
	`

	err := s.db.QueryRow(query, infoHash).Scan(
		&t.ID, &t.PackageID, &t.InfoHash, &t.PieceSize, &t.TotalPieces,
		&t.FileCount, &t.TotalSizeBytes, &t.CreatedByServerID, &t.CreatedAt,
	)

	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Torrent not found", "")
		return
	} else if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query torrent", "")
		return
	}

	respondJSON(w, http.StatusOK, t)
}

// handleDownloadTorrentFile returns the .torrent file
func (s *Server) handleDownloadTorrentFile(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	infoHash := vars["info_hash"]

	var torrentFile []byte
	query := `SELECT torrent_file FROM dcp_torrents WHERE info_hash = $1`

	err := s.db.QueryRow(query, infoHash).Scan(&torrentFile)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Torrent not found", "")
		return
	} else if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query torrent", "")
		return
	}

	w.Header().Set("Content-Type", "application/x-bittorrent")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.torrent", infoHash))
	w.Write(torrentFile)
}

// handleRegisterTorrent registers a new torrent
func (s *Server) handleRegisterTorrent(w http.ResponseWriter, r *http.Request) {
	// Read torrent file from request body
	_, err := ioutil.ReadAll(r.Body)
	if err != nil {
		respondError(w, http.StatusBadRequest, "Failed to read torrent file", "")
		return
	}

	// Parse required fields from query params or headers
	packageID := r.URL.Query().Get("package_id")
	serverID := r.URL.Query().Get("server_id")

	if packageID == "" || serverID == "" {
		respondError(w, http.StatusBadRequest, "Missing package_id or server_id", "")
		return
	}

	// TODO: Parse torrent file and extract metadata
	// For now, return a placeholder response

	respondJSON(w, http.StatusCreated, map[string]string{
		"message": "Torrent registered successfully",
	})
}

// handleListSeeders returns all seeders for a torrent
func (s *Server) handleListSeeders(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	infoHash := vars["info_hash"]

	// Get torrent ID
	var torrentID string
	err := s.db.QueryRow(`SELECT id FROM dcp_torrents WHERE info_hash = $1`, infoHash).Scan(&torrentID)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Torrent not found", "")
		return
	} else if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query torrent", "")
		return
	}

	query := `
		SELECT ts.id, ts.torrent_id, ts.server_id, s.server_name, ts.local_path, 
		       ts.status, ts.uploaded_bytes, ts.last_announce
		FROM torrent_seeders ts
		LEFT JOIN servers s ON ts.server_id = s.id
		WHERE ts.torrent_id = $1 AND ts.status = 'seeding'
		ORDER BY ts.last_announce DESC
	`

	rows, err := s.db.Query(query, torrentID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query seeders", "")
		return
	}
	defer rows.Close()

	var seeders []SeederInfo
	for rows.Next() {
		var si SeederInfo
		var serverName sql.NullString
		err := rows.Scan(&si.ID, &si.TorrentID, &si.ServerID, &serverName, &si.LocalPath,
			&si.Status, &si.UploadedBytes, &si.LastAnnounce)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to scan seeder", "")
			return
		}
		if serverName.Valid {
			si.ServerName = serverName.String
		}
		seeders = append(seeders, si)
	}

	respondJSON(w, http.StatusOK, seeders)
}

// handleRegisterSeeder registers a server as a seeder
func (s *Server) handleRegisterSeeder(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	infoHash := vars["info_hash"]

	var req struct {
		ServerID  string `json:"server_id"`
		LocalPath string `json:"local_path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", "")
		return
	}

	// Get torrent ID
	var torrentID string
	err := s.db.QueryRow(`SELECT id FROM dcp_torrents WHERE info_hash = $1`, infoHash).Scan(&torrentID)
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Torrent not found", "")
		return
	} else if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query torrent", "")
		return
	}

	// Insert or update seeder
	id := uuid.New().String()
	query := `
		INSERT INTO torrent_seeders (id, torrent_id, server_id, local_path, status, last_announce, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'seeding', $5, $6, $7)
		ON CONFLICT (torrent_id, server_id)
		DO UPDATE SET local_path = $4, status = 'seeding', last_announce = $5, updated_at = $7
	`

	now := time.Now()
	_, err = s.db.Exec(query, id, torrentID, req.ServerID, req.LocalPath, now, now, now)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to register seeder", "")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Seeder registered successfully",
	})
}

// handleListTransfers returns all transfers
func (s *Server) handleListTransfers(w http.ResponseWriter, r *http.Request) {
	// Optional filtering
	serverID := r.URL.Query().Get("server_id")
	status := r.URL.Query().Get("status")

	query := `
		SELECT t.id, t.torrent_id, t.source_server_id, t.destination_server_id, t.requested_by,
		       t.status, t.priority, t.progress_percent, t.downloaded_bytes,
		       t.download_speed_bps, t.upload_speed_bps, t.peers_connected, t.eta_seconds,
		       t.error_message, t.started_at, t.completed_at, t.created_at, t.updated_at,
		       dt.package_id, dp.package_name
		FROM transfers t
		LEFT JOIN dcp_torrents dt ON t.torrent_id = dt.id
		LEFT JOIN dcp_packages dp ON dt.package_id = dp.id
		WHERE 1=1
	`
	args := []interface{}{}
	argNum := 1

	if serverID != "" {
		query += fmt.Sprintf(" AND (t.source_server_id = $%d OR t.destination_server_id = $%d)", argNum, argNum)
		args = append(args, serverID)
		argNum++
	}

	if status != "" {
		query += fmt.Sprintf(" AND t.status = $%d", argNum)
		args = append(args, status)
	}

	query += " ORDER BY t.created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query transfers", "")
		return
	}
	defer rows.Close()

	var transfers []TransferInfo
	for rows.Next() {
		var ti TransferInfo
		var sourceServerID, errorMsg, packageID, packageName sql.NullString
		var etaSeconds sql.NullInt64
		var startedAt, completedAt sql.NullTime

		err := rows.Scan(&ti.ID, &ti.TorrentID, &sourceServerID, &ti.DestinationServerID, &ti.RequestedBy,
			&ti.Status, &ti.Priority, &ti.ProgressPercent, &ti.DownloadedBytes,
			&ti.DownloadSpeedBps, &ti.UploadSpeedBps, &ti.PeersConnected, &etaSeconds,
			&errorMsg, &startedAt, &completedAt, &ti.CreatedAt, &ti.UpdatedAt,
			&packageID, &packageName)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to scan transfer", "")
			return
		}

		if sourceServerID.Valid {
			ti.SourceServerID = &sourceServerID.String
		}
		if errorMsg.Valid {
			ti.ErrorMessage = &errorMsg.String
		}
		if etaSeconds.Valid {
			eta := int(etaSeconds.Int64)
			ti.ETASeconds = &eta
		}
		if startedAt.Valid {
			ti.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			ti.CompletedAt = &completedAt.Time
		}
		if packageID.Valid {
			ti.PackageID = packageID.String
		}
		if packageName.Valid {
			ti.PackageName = packageName.String
		}

		transfers = append(transfers, ti)
	}

	respondJSON(w, http.StatusOK, transfers)
}

// handleCreateTransfer creates a new transfer request
func (s *Server) handleCreateTransfer(w http.ResponseWriter, r *http.Request) {
	var req CreateTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", "")
		return
	}

	// Validate required fields
	if req.TorrentID == "" || req.DestinationServerID == "" {
		respondError(w, http.StatusBadRequest, "Missing required fields", "")
		return
	}

	priority := 5
	if req.Priority != nil {
		priority = *req.Priority
	}

	// Create transfer
	id := uuid.New().String()
	query := `
		INSERT INTO transfers (id, torrent_id, destination_server_id, requested_by, status, priority, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'queued', $5, $6, $7)
	`

	now := time.Now()
	_, err := s.db.Exec(query, id, req.TorrentID, req.DestinationServerID, req.RequestedBy, priority, now, now)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create transfer", "")
		return
	}

	respondJSON(w, http.StatusCreated, map[string]string{
		"id":      id,
		"message": "Transfer created successfully",
	})
}

// handleGetTransfer returns a specific transfer
func (s *Server) handleGetTransfer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	transferID := vars["id"]

	var ti TransferInfo
	var sourceServerID, errorMsg sql.NullString
	var etaSeconds sql.NullInt64
	var startedAt, completedAt sql.NullTime

	query := `
		SELECT id, torrent_id, source_server_id, destination_server_id, requested_by,
		       status, priority, progress_percent, downloaded_bytes,
		       download_speed_bps, upload_speed_bps, peers_connected, eta_seconds,
		       error_message, started_at, completed_at, created_at, updated_at
		FROM transfers
		WHERE id = $1
	`

	err := s.db.QueryRow(query, transferID).Scan(
		&ti.ID, &ti.TorrentID, &sourceServerID, &ti.DestinationServerID, &ti.RequestedBy,
		&ti.Status, &ti.Priority, &ti.ProgressPercent, &ti.DownloadedBytes,
		&ti.DownloadSpeedBps, &ti.UploadSpeedBps, &ti.PeersConnected, &etaSeconds,
		&errorMsg, &startedAt, &completedAt, &ti.CreatedAt, &ti.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Transfer not found", "")
		return
	} else if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query transfer", "")
		return
	}

	if sourceServerID.Valid {
		ti.SourceServerID = &sourceServerID.String
	}
	if errorMsg.Valid {
		ti.ErrorMessage = &errorMsg.String
	}
	if etaSeconds.Valid {
		eta := int(etaSeconds.Int64)
		ti.ETASeconds = &eta
	}
	if startedAt.Valid {
		ti.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		ti.CompletedAt = &completedAt.Time
	}

	respondJSON(w, http.StatusOK, ti)
}

// handleUpdateTransfer updates a transfer's progress
func (s *Server) handleUpdateTransfer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	transferID := vars["id"]

	var req UpdateTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", "")
		return
	}

	// Build dynamic update query
	query := "UPDATE transfers SET updated_at = $1"
	args := []interface{}{time.Now()}
	argNum := 2

	if req.Status != nil {
		query += fmt.Sprintf(", status = $%d", argNum)
		args = append(args, *req.Status)
		argNum++

		// Set started_at if status is downloading
		if *req.Status == "downloading" {
			query += fmt.Sprintf(", started_at = COALESCE(started_at, $%d)", argNum)
			args = append(args, time.Now())
			argNum++
		}

		// Set completed_at if status is completed
		if *req.Status == "completed" {
			query += fmt.Sprintf(", completed_at = $%d", argNum)
			args = append(args, time.Now())
			argNum++
		}
	}

	if req.ProgressPercent != nil {
		query += fmt.Sprintf(", progress_percent = $%d", argNum)
		args = append(args, *req.ProgressPercent)
		argNum++
	}

	if req.DownloadedBytes != nil {
		query += fmt.Sprintf(", downloaded_bytes = $%d", argNum)
		args = append(args, *req.DownloadedBytes)
		argNum++
	}

	if req.DownloadSpeedBps != nil {
		query += fmt.Sprintf(", download_speed_bps = $%d", argNum)
		args = append(args, *req.DownloadSpeedBps)
		argNum++
	}

	if req.UploadSpeedBps != nil {
		query += fmt.Sprintf(", upload_speed_bps = $%d", argNum)
		args = append(args, *req.UploadSpeedBps)
		argNum++
	}

	if req.PeersConnected != nil {
		query += fmt.Sprintf(", peers_connected = $%d", argNum)
		args = append(args, *req.PeersConnected)
		argNum++
	}

	if req.ETASeconds != nil {
		query += fmt.Sprintf(", eta_seconds = $%d", argNum)
		args = append(args, *req.ETASeconds)
		argNum++
	}

	if req.ErrorMessage != nil {
		query += fmt.Sprintf(", error_message = $%d", argNum)
		args = append(args, *req.ErrorMessage)
		argNum++
	}

	query += fmt.Sprintf(" WHERE id = $%d", argNum)
	args = append(args, transferID)

	_, err := s.db.Exec(query, args...)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update transfer", "")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Transfer updated successfully",
	})
}

// handleDeleteTransfer cancels/deletes a transfer
func (s *Server) handleDeleteTransfer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	transferID := vars["id"]

	// Update status to cancelled instead of deleting
	query := `UPDATE transfers SET status = 'cancelled', updated_at = $1 WHERE id = $2`
	_, err := s.db.Exec(query, time.Now(), transferID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to cancel transfer", "")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Transfer cancelled successfully",
	})
}

// TorrentQueueItem represents an item in the torrent generation queue
type TorrentQueueItem struct {
	ID                string     `json:"id"`
	PackageID         string     `json:"package_id"`
	PackageName       string     `json:"package_name,omitempty"`
	ServerID          string     `json:"server_id"`
	ServerName        string     `json:"server_name,omitempty"`
	Status            string     `json:"status"`
	ProgressPercent   float64    `json:"progress_percent"`
	CurrentFile       *string    `json:"current_file"`
	ErrorMessage      *string    `json:"error_message"`
	QueuePosition     int        `json:"queue_position"`
	QueuedAt          time.Time  `json:"queued_at"`
	StartedAt         *time.Time `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	ETASeconds        *int       `json:"eta_seconds"`
	HashingSpeedBps   *int64     `json:"hashing_speed_bps,omitempty"`
}

// handleListTorrentQueue returns all items in the torrent generation queue
func (s *Server) handleListTorrentQueue(w http.ResponseWriter, r *http.Request) {
	// Optional server filter
	serverID := r.URL.Query().Get("server_id")
	status := r.URL.Query().Get("status")

	query := `
		SELECT 
			tq.id,
			tq.package_id,
			COALESCE(dp.package_name, '') as package_name,
			tq.server_id,
			COALESCE(srv.name, '') as server_name,
			tq.status,
			tq.progress_percent,
			tq.current_file,
			tq.error_message,
			tq.queued_at,
			tq.started_at,
			tq.completed_at,
			tq.total_size_bytes
		FROM torrent_queue tq
		LEFT JOIN dcp_packages dp ON tq.package_id = dp.id
		LEFT JOIN servers srv ON tq.server_id = srv.id
		WHERE 1=1
	`
	args := []interface{}{}
	argNum := 1

	if serverID != "" {
		query += fmt.Sprintf(" AND tq.server_id = $%d", argNum)
		args = append(args, serverID)
		argNum++
	}

	if status != "" {
		query += fmt.Sprintf(" AND tq.status = $%d", argNum)
		args = append(args, status)
	}

	query += `
		ORDER BY 
			CASE WHEN tq.status = 'generating' THEN 0 
				 WHEN tq.status = 'queued' THEN 1 
				 WHEN tq.status = 'failed' THEN 2
				 ELSE 3 END,
			tq.queued_at ASC
	`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		log.Printf("Error querying torrent queue: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to query torrent queue", "")
		return
	}
	defer rows.Close()

	var queueItems []TorrentQueueItem
	queuePosition := 1
	lastStatus := ""
	
	for rows.Next() {
		var item TorrentQueueItem
		var currentFile, errorMsg sql.NullString
		var startedAt, completedAt sql.NullTime
		var totalSizeBytes sql.NullInt64

		err := rows.Scan(
			&item.ID, &item.PackageID, &item.PackageName, &item.ServerID, &item.ServerName,
			&item.Status, &item.ProgressPercent, &currentFile, &errorMsg,
			&item.QueuedAt, &startedAt, &completedAt, &totalSizeBytes,
		)
		if err != nil {
			log.Printf("Error scanning queue item: %v", err)
			respondError(w, http.StatusInternalServerError, "Failed to scan queue item", "")
			return
		}

		// Reset position counter when status changes
		if item.Status != lastStatus {
			queuePosition = 1
			lastStatus = item.Status
		}
		
		item.QueuePosition = queuePosition
		queuePosition++

		if currentFile.Valid {
			item.CurrentFile = &currentFile.String
		}
		if errorMsg.Valid {
			item.ErrorMessage = &errorMsg.String
		}
		if startedAt.Valid {
			item.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			item.CompletedAt = &completedAt.Time
		}

		// Calculate ETA and hashing speed for items being generated
		if item.Status == "generating" && item.ProgressPercent > 0 && item.StartedAt != nil {
			elapsed := time.Since(*item.StartedAt).Seconds()
			if elapsed > 0 {
				totalEstimated := elapsed / (item.ProgressPercent / 100.0)
				remaining := totalEstimated - elapsed
				if remaining > 0 {
					eta := int(remaining)
					item.ETASeconds = &eta
				}
				// Hashing speed: (progress/100 * total_size) / elapsed = bytes per second
				if totalSizeBytes.Valid && totalSizeBytes.Int64 > 0 {
					bytesSoFar := int64(item.ProgressPercent / 100.0 * float64(totalSizeBytes.Int64))
					speedBps := int64(float64(bytesSoFar) / elapsed)
					item.HashingSpeedBps = &speedBps
				}
			}
		}

		queueItems = append(queueItems, item)
	}

	if queueItems == nil {
		queueItems = []TorrentQueueItem{}
	}

	respondJSON(w, http.StatusOK, queueItems)
}

// UpdateQueuePositionRequest represents request to change queue order
type UpdateQueuePositionRequest struct {
	NewPosition int `json:"new_position"`
}

// handleUpdateQueuePosition changes the position of a queue item
func (s *Server) handleUpdateQueuePosition(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	queueItemID := vars["id"]

	var req UpdateQueuePositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", "")
		return
	}

	// Get current queue item details
	var currentItem struct {
		serverID  string
		status    string
		queuedAt  time.Time
	}
	
	err := s.db.QueryRow(`
		SELECT server_id, status, queued_at 
		FROM torrent_queue 
		WHERE id = $1
	`, queueItemID).Scan(&currentItem.serverID, &currentItem.status, &currentItem.queuedAt)
	
	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Queue item not found", "")
		return
	} else if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query queue item", "")
		return
	}

	// Only allow reordering of queued items
	if currentItem.status != "queued" {
		respondError(w, http.StatusBadRequest, "Can only reorder queued items", "")
		return
	}

	// Get all queued items for this server to determine new queued_at time
	rows, err := s.db.Query(`
		SELECT id, queued_at 
		FROM torrent_queue 
		WHERE server_id = $1 AND status = 'queued'
		ORDER BY queued_at ASC
	`, currentItem.serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query queue", "")
		return
	}
	defer rows.Close()

	var queuedItems []struct {
		id       string
		queuedAt time.Time
	}
	for rows.Next() {
		var item struct {
			id       string
			queuedAt time.Time
		}
		rows.Scan(&item.id, &item.queuedAt)
		queuedItems = append(queuedItems, item)
	}

	// Validate new position
	if req.NewPosition < 1 || req.NewPosition > len(queuedItems) {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Invalid position. Must be between 1 and %d", len(queuedItems)), "")
		return
	}

	// Calculate new queued_at time based on position
	targetPosition := req.NewPosition - 1 // Convert to 0-based index
	var newQueuedAt time.Time
	
	if targetPosition == 0 {
		// Move to front - set time before first item
		newQueuedAt = queuedItems[0].queuedAt.Add(-time.Minute)
	} else if targetPosition == len(queuedItems)-1 {
		// Move to end - set time after last item
		newQueuedAt = queuedItems[len(queuedItems)-1].queuedAt.Add(time.Minute)
	} else {
		// Move between items - set time between target-1 and target
		before := queuedItems[targetPosition-1].queuedAt
		after := queuedItems[targetPosition].queuedAt
		newQueuedAt = before.Add(after.Sub(before) / 2)
	}

	// Update the queued_at time
	_, err = s.db.Exec(`
		UPDATE torrent_queue 
		SET queued_at = $1 
		WHERE id = $2
	`, newQueuedAt, queueItemID)
	
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update queue position", "")
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":      "Queue position updated successfully",
		"new_position": req.NewPosition,
	})
}

// handleRetryQueueItem retries a failed queue item
func (s *Server) handleRetryQueueItem(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	queueItemID := vars["id"]

	query := `
		UPDATE torrent_queue
		SET status = 'queued', 
		    progress_percent = 0, 
		    error_message = NULL, 
		    current_file = NULL,
		    started_at = NULL,
		    completed_at = NULL,
		    queued_at = $1
		WHERE id = $2 AND status = 'failed'
	`

	result, err := s.db.Exec(query, time.Now(), queueItemID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retry queue item", "")
		return
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		respondError(w, http.StatusNotFound, "Queue item not found or not in failed state", "")
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Queue item retry scheduled successfully",
	})
}

// handleClearCompletedQueue removes all completed queue items
func (s *Server) handleClearCompletedQueue(w http.ResponseWriter, r *http.Request) {
	query := `DELETE FROM torrent_queue WHERE status = 'completed'`

	result, err := s.db.Exec(query)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to clear completed items", "")
		return
	}

	affected, _ := result.RowsAffected()
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Completed items cleared successfully",
		"count":   affected,
	})
}
