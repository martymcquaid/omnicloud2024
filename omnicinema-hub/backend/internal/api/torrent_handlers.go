package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/lib/pq"
	"github.com/omnicloud/omnicloud/internal/torrent"
)

// ServerTorrentStatusItem is per-server status/error for a torrent (for UI)
type ServerTorrentStatusItem struct {
	ServerID     string `json:"server_id"`
	ServerName   string `json:"server_name,omitempty"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// TorrentInfo represents torrent metadata
type TorrentInfo struct {
	ID                string                    `json:"id"`
	PackageID         string                    `json:"package_id"`
	InfoHash          string                    `json:"info_hash"`
	PieceSize         int                       `json:"piece_size"`
	TotalPieces       int                       `json:"total_pieces"`
	FileCount         int                       `json:"file_count"`
	TotalSizeBytes    int64                     `json:"total_size_bytes"`
	CreatedByServerID string                    `json:"created_by_server_id"`
	CreatedAt         time.Time                 `json:"created_at"`
	SeedersCount      int                       `json:"seeders_count"`
	ServerStatuses    []ServerTorrentStatusItem `json:"server_statuses,omitempty"`
}

// SeederInfo represents a seeder for a torrent
type SeederInfo struct {
	ID            string    `json:"id"`
	TorrentID     string    `json:"torrent_id"`
	ServerID      string    `json:"server_id"`
	ServerName    string    `json:"server_name,omitempty"`
	LocalPath     string    `json:"local_path"`
	Status        string    `json:"status"`
	UploadedBytes int64     `json:"uploaded_bytes"`
	LastAnnounce  time.Time `json:"last_announce"`
}

type AnnounceAttemptInfo struct {
	InfoHash      string    `json:"info_hash"`
	PeerID        string    `json:"peer_id"`
	IP            string    `json:"ip"`
	Port          int       `json:"port"`
	Event         string    `json:"event"`
	Status        string    `json:"status"`
	FailureReason string    `json:"failure_reason,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type TrackerLiveTorrent struct {
	ID                     string     `json:"id"`
	PackageID              string     `json:"package_id"`
	InfoHash               string     `json:"info_hash"`
	CreatedAt              time.Time  `json:"created_at"`
	Active                 bool       `json:"active"`
	Seeders                int        `json:"seeders"`
	Leechers               int        `json:"leechers"`
	PeersCount             int        `json:"peers_count"`
	LastAnnounce           *time.Time `json:"last_announce,omitempty"`
	RecentAttempts15m      int        `json:"recent_attempts_15m"`
	RecentErrorAttempts15m int        `json:"recent_error_attempts_15m"`
	LastAttemptAt          *time.Time `json:"last_attempt_at,omitempty"`
}

type TrackerLiveResponse struct {
	TrackerAvailable bool                    `json:"tracker_available"`
	IntervalSec      int                     `json:"interval_sec"`
	ActiveSwarms     int                     `json:"active_swarms"`
	TotalLivePeers   int                     `json:"total_live_peers"`
	TotalTorrents    int                     `json:"total_torrents"`
	GeneratedAt      time.Time               `json:"generated_at"`
	ActiveSwarmPeers []torrent.SwarmSnapshot `json:"active_swarm_peers"`
	Torrents         []TrackerLiveTorrent    `json:"torrents"`
}

// TransferInfo represents a DCP transfer
type TransferInfo struct {
	ID                  string     `json:"id"`
	TorrentID           string     `json:"torrent_id"`
	PackageID           string     `json:"package_id,omitempty"`
	PackageName         string     `json:"package_name,omitempty"`
	SourceServerID      *string    `json:"source_server_id"`
	DestinationServerID string     `json:"destination_server_id"`
	RequestedBy         string     `json:"requested_by"`
	Status              string     `json:"status"`
	Priority            int        `json:"priority"`
	ProgressPercent     float64    `json:"progress_percent"`
	DownloadedBytes     int64      `json:"downloaded_bytes"`
	DownloadSpeedBps    int64      `json:"download_speed_bps"`
	UploadSpeedBps      int64      `json:"upload_speed_bps"`
	PeersConnected      int        `json:"peers_connected"`
	ETASeconds          *int       `json:"eta_seconds"`
	ErrorMessage        *string    `json:"error_message"`
	StartedAt           *time.Time `json:"started_at"`
	CompletedAt         *time.Time `json:"completed_at"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// CreateTransferRequest represents a request to create a transfer
type CreateTransferRequest struct {
	TorrentID           string `json:"torrent_id"`
	DestinationServerID string `json:"destination_server_id"`
	RequestedBy         string `json:"requested_by"`
	Priority            *int   `json:"priority"`
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
		       t.file_count, t.total_size_bytes, t.created_by_server_id, t.created_at,
		       (SELECT COUNT(*) FROM torrent_seeders ts2 WHERE ts2.torrent_id = t.id AND ts2.status IN ('seeding','completed')) AS seeders_count
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
			&t.FileCount, &t.TotalSizeBytes, &t.CreatedByServerID, &t.CreatedAt, &t.SeedersCount)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to scan torrent", "")
			return
		}
		torrents = append(torrents, t)
	}

	// Attach per-server status/errors for UI
	if len(torrents) > 0 {
		ids := make([]string, 0, len(torrents))
		for i := range torrents {
			ids = append(ids, torrents[i].ID)
		}
		statusQuery := `
			SELECT sts.torrent_id, sts.server_id, COALESCE(s.name, '') AS server_name, sts.status, COALESCE(sts.error_message, '')
			FROM server_torrent_status sts
			LEFT JOIN servers s ON s.id = sts.server_id
			WHERE sts.torrent_id = ANY($1)
		`
		statusRows, err := s.db.Query(statusQuery, pq.Array(ids))
		if err == nil {
			byTorrent := make(map[string][]ServerTorrentStatusItem)
			for statusRows.Next() {
				var torrentID, serverID, serverName, status, errMsg string
				if err := statusRows.Scan(&torrentID, &serverID, &serverName, &status, &errMsg); err != nil {
					continue
				}
				byTorrent[torrentID] = append(byTorrent[torrentID], ServerTorrentStatusItem{
					ServerID: serverID, ServerName: serverName, Status: status, ErrorMessage: errMsg,
				})
			}
			statusRows.Close()
			for i := range torrents {
				torrents[i].ServerStatuses = byTorrent[torrents[i].ID]
			}
		}
	}

	respondJSON(w, http.StatusOK, torrents)
}

// handleGetTorrent returns a specific torrent by info hash
func (s *Server) handleGetTorrent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	infoHash := vars["info_hash"]

	var t TorrentInfo
	query := `
		SELECT t.id, t.package_id, t.info_hash, t.piece_size, t.total_pieces, 
		       t.file_count, t.total_size_bytes, t.created_by_server_id, t.created_at,
		       (SELECT COUNT(*) FROM torrent_seeders ts2 WHERE ts2.torrent_id = t.id AND ts2.status IN ('seeding','completed')) AS seeders_count
		FROM dcp_torrents t
		WHERE t.info_hash = $1
	`

	err := s.db.QueryRow(query, infoHash).Scan(
		&t.ID, &t.PackageID, &t.InfoHash, &t.PieceSize, &t.TotalPieces,
		&t.FileCount, &t.TotalSizeBytes, &t.CreatedByServerID, &t.CreatedAt, &t.SeedersCount,
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

// handleDownloadTorrentFile returns the .torrent file in standard format for external clients
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

	// Convert to standard format for external clients (always do this)
	standardFile, err := torrent.ConvertToStandardFormat(torrentFile, "")
	if err != nil {
		log.Printf("Torrent file convert to standard format failed (info_hash=%s), serving original: %v", infoHash, err)
	} else {
		torrentFile = standardFile
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
		SELECT ts.id, ts.torrent_id, ts.server_id, s.name, ts.local_path, 
		       ts.status, ts.uploaded_bytes, ts.last_announce
		FROM torrent_seeders ts
		LEFT JOIN servers s ON ts.server_id = s.id
		WHERE ts.torrent_id = $1 AND ts.status IN ('seeding', 'completed')
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
	torrentID := r.URL.Query().Get("torrent_id")

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
		argNum++
	}

	if torrentID != "" {
		query += fmt.Sprintf(" AND t.torrent_id = $%d", argNum)
		args = append(args, torrentID)
		argNum++
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

// handleListAnnounceAttempts returns recent tracker announce attempts for a torrent.
func (s *Server) handleListAnnounceAttempts(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	infoHash := vars["info_hash"]

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			if n < 1 {
				n = 1
			}
			if n > 500 {
				n = 500
			}
			limit = n
		}
	}

	rows, err := s.db.Query(`
		SELECT COALESCE(info_hash, ''), COALESCE(peer_id, ''), COALESCE(ip, ''), COALESCE(port, 0),
		       COALESCE(event, ''), status, COALESCE(failure_reason, ''), created_at
		FROM torrent_announce_attempts
		WHERE info_hash = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, infoHash, limit)
	if err != nil {
		// If migration isn't present yet, return empty list instead of hard-failing UI.
		respondJSON(w, http.StatusOK, []AnnounceAttemptInfo{})
		return
	}
	defer rows.Close()

	out := make([]AnnounceAttemptInfo, 0)
	for rows.Next() {
		var item AnnounceAttemptInfo
		if scanErr := rows.Scan(
			&item.InfoHash,
			&item.PeerID,
			&item.IP,
			&item.Port,
			&item.Event,
			&item.Status,
			&item.FailureReason,
			&item.CreatedAt,
		); scanErr != nil {
			continue
		}
		out = append(out, item)
	}
	respondJSON(w, http.StatusOK, out)
}

// handleTrackerLive returns tracker-wide live state with active swarm peers and known torrent rollup.
func (s *Server) handleTrackerLive(w http.ResponseWriter, r *http.Request) {
	if s.tracker == nil {
		respondJSON(w, http.StatusOK, TrackerLiveResponse{
			TrackerAvailable: false,
			GeneratedAt:      time.Now(),
			ActiveSwarmPeers: []torrent.SwarmSnapshot{},
			Torrents:         []TrackerLiveTorrent{},
		})
		return
	}

	snap := s.tracker.GetSnapshot()
	swarmByHash := make(map[string]torrent.SwarmSnapshot, len(snap.Swarms))
	for _, swarm := range snap.Swarms {
		swarmByHash[swarm.InfoHash] = swarm
	}

	type attemptsAgg struct {
		recentAttempts int
		recentErrors   int
		lastAttemptAt  *time.Time
	}
	attemptsByHash := make(map[string]attemptsAgg)
	attemptRows, err := s.db.Query(`
		SELECT info_hash,
		       COUNT(*) FILTER (WHERE created_at >= NOW() - INTERVAL '15 minutes')::int AS recent_attempts,
		       COUNT(*) FILTER (WHERE status = 'error' AND created_at >= NOW() - INTERVAL '15 minutes')::int AS recent_errors,
		       MAX(created_at) AS last_attempt
		FROM torrent_announce_attempts
		WHERE info_hash IS NOT NULL
		GROUP BY info_hash
	`)
	if err == nil {
		defer attemptRows.Close()
		for attemptRows.Next() {
			var hash string
			var recentAttempts, recentErrors int
			var lastAttempt sql.NullTime
			if scanErr := attemptRows.Scan(&hash, &recentAttempts, &recentErrors, &lastAttempt); scanErr != nil {
				continue
			}
			var ptr *time.Time
			if lastAttempt.Valid {
				ts := lastAttempt.Time
				ptr = &ts
			}
			attemptsByHash[hash] = attemptsAgg{
				recentAttempts: recentAttempts,
				recentErrors:   recentErrors,
				lastAttemptAt:  ptr,
			}
		}
	}

	rows, err := s.db.Query(`
		SELECT t.id, t.package_id, t.info_hash, t.created_at
		FROM dcp_torrents t
		ORDER BY t.created_at DESC
	`)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query torrents", "")
		return
	}
	defer rows.Close()

	torrents := make([]TrackerLiveTorrent, 0)
	for rows.Next() {
		var item TrackerLiveTorrent
		if scanErr := rows.Scan(&item.ID, &item.PackageID, &item.InfoHash, &item.CreatedAt); scanErr != nil {
			continue
		}
		if swarm, exists := swarmByHash[item.InfoHash]; exists {
			item.Active = true
			item.Seeders = swarm.Seeders
			item.Leechers = swarm.Leechers
			item.PeersCount = swarm.PeersCount
			item.LastAnnounce = swarm.LastAnnounce
		}
		if agg, exists := attemptsByHash[item.InfoHash]; exists {
			item.RecentAttempts15m = agg.recentAttempts
			item.RecentErrorAttempts15m = agg.recentErrors
			item.LastAttemptAt = agg.lastAttemptAt
		}
		torrents = append(torrents, item)
	}

	respondJSON(w, http.StatusOK, TrackerLiveResponse{
		TrackerAvailable: true,
		IntervalSec:      snap.IntervalSec,
		ActiveSwarms:     snap.ActiveSwarms,
		TotalLivePeers:   snap.TotalPeers,
		TotalTorrents:    len(torrents),
		GeneratedAt:      snap.GeneratedAt,
		ActiveSwarmPeers: snap.Swarms,
		Torrents:         torrents,
	})
}

type TorrentQueueItemInfo struct {
	ID              string     `json:"id"`
	PackageID       string     `json:"package_id"`
	PackageName     string     `json:"package_name,omitempty"`
	ServerID        string     `json:"server_id"`
	ServerName      string     `json:"server_name,omitempty"`
	Status          string     `json:"status"`
	ProgressPercent float64    `json:"progress_percent"`
	CurrentFile     *string    `json:"current_file,omitempty"`
	ErrorMessage    *string    `json:"error_message,omitempty"`
	QueuedAt        time.Time  `json:"queued_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	TotalSizeBytes  *int64     `json:"total_size_bytes,omitempty"`
	HashingSpeedBps *int64     `json:"hashing_speed_bps,omitempty"`
}

func (s *Server) handleListTorrentQueue(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`
		SELECT tq.id, tq.package_id, COALESCE(p.package_name, 'Package ' || LEFT(tq.package_id::text, 8)) AS package_name,
		       tq.server_id, COALESCE(NULLIF(TRIM(s.name), ''), 'Server ' || LEFT(tq.server_id::text, 8)) AS server_name,
		       tq.status, tq.progress_percent, tq.current_file, tq.error_message, tq.queued_at, tq.started_at, tq.completed_at,
		       tq.total_size_bytes, tq.hashing_speed_bps
		FROM torrent_queue tq
		LEFT JOIN servers s ON s.id = tq.server_id
		LEFT JOIN dcp_packages p ON p.id = tq.package_id
		ORDER BY tq.queued_at DESC
	`)
	if err != nil {
		respondJSON(w, http.StatusOK, []TorrentQueueItemInfo{})
		return
	}
	defer rows.Close()

	out := make([]TorrentQueueItemInfo, 0)
	for rows.Next() {
		var item TorrentQueueItemInfo
		var currentFile, errorMessage sql.NullString
		var startedAt, completedAt sql.NullTime
		var totalSizeBytes, hashingSpeedBps sql.NullInt64
		if scanErr := rows.Scan(
			&item.ID,
			&item.PackageID,
			&item.PackageName,
			&item.ServerID,
			&item.ServerName,
			&item.Status,
			&item.ProgressPercent,
			&currentFile,
			&errorMessage,
			&item.QueuedAt,
			&startedAt,
			&completedAt,
			&totalSizeBytes,
			&hashingSpeedBps,
		); scanErr != nil {
			continue
		}
		if currentFile.Valid {
			item.CurrentFile = &currentFile.String
		}
		if errorMessage.Valid {
			item.ErrorMessage = &errorMessage.String
		}
		if startedAt.Valid {
			item.StartedAt = &startedAt.Time
		}
		if completedAt.Valid {
			item.CompletedAt = &completedAt.Time
		}
		if totalSizeBytes.Valid {
			item.TotalSizeBytes = &totalSizeBytes.Int64
		}
		if hashingSpeedBps.Valid {
			item.HashingSpeedBps = &hashingSpeedBps.Int64
		}
		out = append(out, item)
	}
	respondJSON(w, http.StatusOK, out)
}

// PendingTransferInfo represents a transfer that a client needs to download
type PendingTransferInfo struct {
	ID             string `json:"id"`
	TorrentID      string `json:"torrent_id"`
	InfoHash       string `json:"info_hash"`
	PackageID      string `json:"package_id"`
	PackageName    string `json:"package_name"`
	Status         string `json:"status"`
	TotalSizeBytes int64  `json:"total_size_bytes"`
	Priority       int    `json:"priority"`
}

// handleGetPendingTransfers returns transfers assigned to a specific server that need downloading
func (s *Server) handleGetPendingTransfers(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	query := `
		SELECT t.id, t.torrent_id, dt.info_hash, dt.package_id,
		       COALESCE(dp.package_name, ''), t.status, COALESCE(dt.total_size_bytes, 0), t.priority
		FROM transfers t
		JOIN dcp_torrents dt ON t.torrent_id = dt.id
		LEFT JOIN dcp_packages dp ON dt.package_id = dp.id
		WHERE t.destination_server_id = $1 AND t.status IN ('queued', 'downloading')
		ORDER BY t.priority ASC, t.created_at ASC
	`

	rows, err := s.db.Query(query, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query pending transfers", err.Error())
		return
	}
	defer rows.Close()

	transfers := make([]PendingTransferInfo, 0)
	for rows.Next() {
		var pt PendingTransferInfo
		if err := rows.Scan(&pt.ID, &pt.TorrentID, &pt.InfoHash, &pt.PackageID,
			&pt.PackageName, &pt.Status, &pt.TotalSizeBytes, &pt.Priority); err != nil {
			log.Printf("Error scanning pending transfer: %v", err)
			continue
		}
		transfers = append(transfers, pt)
	}

	respondJSON(w, http.StatusOK, transfers)
}

// handleTorrentQueueCheck returns whether a package is already queued or being hashed by any server.
// Used by client servers to avoid starting a hash when another server (e.g. main) is already doing it.
func (s *Server) handleTorrentQueueCheck(w http.ResponseWriter, r *http.Request) {
	packageID := r.URL.Query().Get("package_id")
	if packageID == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "package_id required"})
		return
	}
	if _, err := uuid.Parse(packageID); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid package_id"})
		return
	}
	var alreadyInProgress, torrentExists bool
	err := s.db.QueryRow(`
		SELECT EXISTS(
			SELECT 1 FROM torrent_queue
			WHERE package_id = $1::uuid AND status IN ('queued', 'generating')
		),
		EXISTS(SELECT 1 FROM dcp_torrents WHERE package_id = $1::uuid)
	`, packageID).Scan(&alreadyInProgress, &torrentExists)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"already_in_progress": alreadyInProgress,
		"torrent_exists":      torrentExists,
	})
}
