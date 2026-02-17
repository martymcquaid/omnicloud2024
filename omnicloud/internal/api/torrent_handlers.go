package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
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
	PackageName       string                    `json:"package_name,omitempty"`
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

// AnnounceAttemptInfo represents a tracker announce attempt for a torrent.
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

// TrackerLiveTorrent summarizes tracker visibility for a known torrent.
type TrackerLiveTorrent struct {
	ID                     string     `json:"id"`
	PackageID              string     `json:"package_id"`
	PackageName            string     `json:"package_name,omitempty"`
	ContentTitle           string     `json:"content_title,omitempty"`
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

// TrackerLiveResponse is the payload for GET /tracker/live.
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
	ID                    string     `json:"id"`
	TorrentID             string     `json:"torrent_id"`
	PackageID             string     `json:"package_id,omitempty"`
	PackageName           string     `json:"package_name,omitempty"`
	SourceServerID        *string    `json:"source_server_id"`
	SourceServerName      string     `json:"source_server_name,omitempty"`
	DestinationServerID   string     `json:"destination_server_id"`
	DestinationServerName string     `json:"destination_server_name,omitempty"`
	RequestedBy           string     `json:"requested_by"`
	Status                string     `json:"status"`
	Priority              int        `json:"priority"`
	ProgressPercent       float64    `json:"progress_percent"`
	DownloadedBytes       int64      `json:"downloaded_bytes"`
	TotalSizeBytes        int64      `json:"total_size_bytes"`
	DownloadSpeedBps      int64      `json:"download_speed_bps"`
	UploadSpeedBps        int64      `json:"upload_speed_bps"`
	PeersConnected        int        `json:"peers_connected"`
	ETASeconds            *int       `json:"eta_seconds"`
	ErrorMessage          *string    `json:"error_message"`
	StartedAt             *time.Time `json:"started_at"`
	CompletedAt           *time.Time `json:"completed_at"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
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
		SELECT t.id, t.package_id, COALESCE(dp.package_name, ''), t.info_hash, t.piece_size, t.total_pieces,
		       t.file_count, t.total_size_bytes, t.created_by_server_id, t.created_at,
		       (SELECT COUNT(*) FROM torrent_seeders ts2 WHERE ts2.torrent_id = t.id AND ts2.status IN ('seeding','completed')) AS seeders_count
		FROM dcp_torrents t
		LEFT JOIN dcp_packages dp ON dp.id = t.package_id
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
		err := rows.Scan(&t.ID, &t.PackageID, &t.PackageName, &t.InfoHash, &t.PieceSize, &t.TotalPieces,
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

	// #region agent log
	var totalSeeders int
	for i := range torrents {
		totalSeeders += torrents[i].SeedersCount
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"location": "torrent_handlers.go:handleListTorrents", "message": "list torrents", "hypothesisId": "H_list",
		"timestamp": time.Now().UnixNano() / 1e6, "data": map[string]interface{}{"total_torrents": len(torrents), "total_seeders_count": totalSeeders},
	})
	if f, _ := os.OpenFile("/home/appbox/DCPCLOUDAPP/.cursor/debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); f != nil {
		f.Write(append(payload, '\n'))
		f.Close()
	}
	// #endregion

	respondJSON(w, http.StatusOK, torrents)
}

// handleGetTorrent returns a specific torrent by info hash
func (s *Server) handleGetTorrent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	infoHash := vars["info_hash"]

	var t TorrentInfo
	query := `
		SELECT t.id, t.package_id, COALESCE(dp.package_name, ''), t.info_hash, t.piece_size, t.total_pieces,
		       t.file_count, t.total_size_bytes, t.created_by_server_id, t.created_at,
		       (SELECT COUNT(*) FROM torrent_seeders ts2 WHERE ts2.torrent_id = t.id AND ts2.status IN ('seeding','completed')) AS seeders_count
		FROM dcp_torrents t
		LEFT JOIN dcp_packages dp ON dp.id = t.package_id
		WHERE t.info_hash = $1
	`

	err := s.db.QueryRow(query, infoHash).Scan(
		&t.ID, &t.PackageID, &t.PackageName, &t.InfoHash, &t.PieceSize, &t.TotalPieces,
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

// handleDownloadTorrentFile returns the .torrent file. When s.trackerPort is set, the announce URL
// is rewritten to use the request host with the tracker port so clients reach the main server's tracker.
// The file is always returned in STANDARD format (info as dict) for external client compatibility.
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

	// Rewrite announce URL if trackerPort is set
	announceURL := ""
	if s.trackerPort > 0 && r.Host != "" {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		trackerHost := net.JoinHostPort(host, strconv.Itoa(s.trackerPort))
		announceURL = "http://" + trackerHost + "/announce"
	}

	// Convert to standard format for external clients (always do this; stored format is internal-only)
	standardFile, err := torrent.RewriteTorrentAnnounceWithRawInfo(torrentFile, announceURL)
	if err != nil {
		log.Printf("Torrent file convert to standard format failed (info_hash=%s), serving original: %v", infoHash, err)
		// Fallback to original (may still work with some clients or for internal use)
	} else {
		torrentFile = standardFile
	}

	w.Header().Set("Content-Type", "application/x-bittorrent")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.torrent", infoHash))
	w.Write(torrentFile)
}

// RegisterTorrentRequest is the JSON payload from client servers uploading a torrent
type RegisterTorrentRequest struct {
	AssetMapUUID   string `json:"assetmap_uuid"`
	InfoHash       string `json:"info_hash"`
	TorrentFile    []byte `json:"torrent_file"` // base64-decoded by json.Unmarshal
	PieceSize      int    `json:"piece_size"`
	TotalPieces    int    `json:"total_pieces"`
	FileCount      int    `json:"file_count"`
	TotalSizeBytes int64  `json:"total_size_bytes"`
	ServerID       string `json:"server_id"`
}

// handleRegisterTorrent accepts a torrent file upload from a client server.
// The client sends the torrent file bytes + metadata after hashing completes.
// The main server resolves the package by assetmap_uuid, stores the torrent,
// and registers the uploading server as a seeder.
func (s *Server) handleRegisterTorrent(w http.ResponseWriter, r *http.Request) {
	var req RegisterTorrentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[torrent-register] Failed to decode request body: %v", err)
		respondError(w, http.StatusBadRequest, "Failed to decode request body", err.Error())
		return
	}

	// Validate required fields
	if req.AssetMapUUID == "" || req.InfoHash == "" || len(req.TorrentFile) == 0 || req.ServerID == "" {
		log.Printf("[torrent-register] Missing required fields: assetmap_uuid=%q info_hash=%q torrent_file_len=%d server_id=%q",
			req.AssetMapUUID, req.InfoHash, len(req.TorrentFile), req.ServerID)
		respondError(w, http.StatusBadRequest, "Missing required fields (assetmap_uuid, info_hash, torrent_file, server_id)", "")
		return
	}

	log.Printf("[torrent-register] Received torrent upload: assetmap_uuid=%s info_hash=%s file_size=%d bytes total_size=%d from server=%s",
		req.AssetMapUUID, req.InfoHash, len(req.TorrentFile), req.TotalSizeBytes, req.ServerID)

	// Resolve assetmap_uuid to package_id on the main server
	var packageID string
	err := s.db.QueryRow("SELECT id FROM dcp_packages WHERE assetmap_uuid = $1", req.AssetMapUUID).Scan(&packageID)
	if err == sql.ErrNoRows {
		log.Printf("[torrent-register] Package not found for assetmap_uuid=%s - metadata may not have synced yet", req.AssetMapUUID)
		respondError(w, http.StatusNotFound, "Package not found for assetmap_uuid", req.AssetMapUUID)
		return
	} else if err != nil {
		log.Printf("[torrent-register] Failed to query package for assetmap_uuid=%s: %v", req.AssetMapUUID, err)
		respondError(w, http.StatusInternalServerError, "Failed to query package", err.Error())
		return
	}

	log.Printf("[torrent-register] Resolved assetmap_uuid=%s to package_id=%s", req.AssetMapUUID, packageID)

	// Insert into dcp_torrents (upsert on info_hash to handle re-uploads)
	torrentID := uuid.New().String()
	query := `
		INSERT INTO dcp_torrents (id, package_id, info_hash, torrent_file, piece_size, total_pieces,
		                          created_by_server_id, file_count, total_size_bytes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (info_hash) DO UPDATE SET
			torrent_file = EXCLUDED.torrent_file,
			piece_size = EXCLUDED.piece_size,
			total_pieces = EXCLUDED.total_pieces,
			file_count = EXCLUDED.file_count,
			total_size_bytes = EXCLUDED.total_size_bytes
		RETURNING id
	`

	err = s.db.QueryRow(query,
		torrentID, packageID, req.InfoHash, req.TorrentFile, req.PieceSize, req.TotalPieces,
		req.ServerID, req.FileCount, req.TotalSizeBytes, time.Now(),
	).Scan(&torrentID)
	if err != nil {
		log.Printf("[torrent-register] Failed to save torrent to database: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to save torrent", err.Error())
		return
	}

	log.Printf("[torrent-register] Saved torrent to database: torrent_id=%s package_id=%s info_hash=%s", torrentID, packageID, req.InfoHash)

	// Register the uploading server as a seeder
	seederID := uuid.New().String()
	now := time.Now()
	seederQuery := `
		INSERT INTO torrent_seeders (id, torrent_id, server_id, local_path, status, last_announce, created_at, updated_at)
		VALUES ($1, $2, $3, '', 'seeding', $4, $5, $6)
		ON CONFLICT (torrent_id, server_id)
		DO UPDATE SET status = 'seeding', last_announce = $4, updated_at = $6
	`
	_, err = s.db.Exec(seederQuery, seederID, torrentID, req.ServerID, now, now, now)
	if err != nil {
		log.Printf("[torrent-register] Warning: failed to register seeder: %v", err)
		// Don't fail the whole request - the torrent was saved successfully
	} else {
		log.Printf("[torrent-register] Registered server %s as seeder for torrent %s", req.ServerID, req.InfoHash)
	}

	// Update queue status on the main server if there's a queue entry for this package
	_, err = s.db.Exec(`
		UPDATE torrent_queue SET status = 'completed', progress_percent = 100, completed_at = $1
		WHERE package_id = $2 AND server_id = $3 AND status IN ('queued', 'generating')
	`, now, packageID, req.ServerID)
	if err != nil {
		log.Printf("[torrent-register] Warning: failed to update queue status: %v", err)
	}

	log.Printf("[torrent-register] SUCCESS: Torrent registered for package %s (assetmap_uuid=%s, info_hash=%s) from server %s",
		packageID, req.AssetMapUUID, req.InfoHash, req.ServerID)

	respondJSON(w, http.StatusCreated, map[string]string{
		"message":    "Torrent registered successfully",
		"torrent_id": torrentID,
		"package_id": packageID,
		"info_hash":  req.InfoHash,
	})
}

// handleMissingTorrents returns assetmap_uuids for packages that this server has in
// inventory but no torrent exists on the main server. The client uses this to know
// which locally-generated torrents need to be uploaded.
func (s *Server) handleMissingTorrents(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID := vars["id"]

	// Find packages where: this server has inventory, but NO torrent exists in dcp_torrents
	query := `
		SELECT p.assetmap_uuid
		FROM server_dcp_inventory inv
		JOIN dcp_packages p ON p.id = inv.package_id
		WHERE inv.server_id = $1
		  AND inv.status = 'online'
		  AND NOT EXISTS (
		    SELECT 1 FROM dcp_torrents dt WHERE dt.package_id = inv.package_id
		  )
	`

	rows, err := s.db.Query(query, serverID)
	if err != nil {
		log.Printf("[missing-torrents] Error querying missing torrents for server %s: %v", serverID, err)
		respondError(w, http.StatusInternalServerError, "Failed to query missing torrents", "")
		return
	}
	defer rows.Close()

	var uuids []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			continue
		}
		uuids = append(uuids, uuid)
	}

	if len(uuids) == 0 {
		uuids = []string{}
	}

	log.Printf("[missing-torrents] Server %s: %d packages in inventory without torrents", serverID, len(uuids))

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"missing_assetmap_uuids": uuids,
		"count":                  len(uuids),
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
		log.Printf("List seeders query failed (torrent_id=%s): %v", torrentID, err)
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
			log.Printf("List seeders scan failed: %v", err)
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

// TorrentPeerStatus is the response for GET /torrents/{info_hash}/peer-status (verify tracker/seeding).
type TorrentPeerStatus struct {
	InfoHash         string     `json:"info_hash"`
	SeedersCount     int        `json:"seeders_count"`
	AnnounceAttempts int        `json:"announce_attempts_recent"`
	LastOkAnnounceAt *time.Time `json:"last_ok_announce_at,omitempty"`
	IPsSeen          []string   `json:"ips_seen,omitempty"`
	Ok               bool       `json:"ok"` // true if at least one successful announce recently
	Message          string     `json:"message"`
}

// handleTorrentPeerStatus returns a short verification summary: seeders count and recent tracker announces.
// Use this to verify the main server is announcing to the tracker (e.g. for "71 voice test").
func (s *Server) handleTorrentPeerStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	infoHash := vars["info_hash"]

	// Seeders count
	var seedersCount int
	_ = s.db.QueryRow(`
		SELECT COUNT(*) FROM torrent_seeders ts
		JOIN dcp_torrents t ON t.id = ts.torrent_id
		WHERE t.info_hash = $1 AND ts.status IN ('seeding','completed')
	`, infoHash).Scan(&seedersCount)

	// Recent announce attempts (last 50)
	rows, err := s.db.Query(`
		SELECT status, ip, created_at
		FROM torrent_announce_attempts
		WHERE info_hash = $1
		ORDER BY created_at DESC
		LIMIT 50
	`, infoHash)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query announce attempts", "")
		return
	}
	defer rows.Close()

	var announceAttempts int
	var lastOk *time.Time
	ipSet := make(map[string]struct{})
	for rows.Next() {
		var status, ip string
		var createdAt time.Time
		if err := rows.Scan(&status, &ip, &createdAt); err != nil {
			continue
		}
		announceAttempts++
		if ip != "" {
			ipSet[ip] = struct{}{}
		}
		if status == "ok" && lastOk == nil {
			t := createdAt
			lastOk = &t
		}
	}
	ipsSeen := make([]string, 0, len(ipSet))
	for k := range ipSet {
		ipsSeen = append(ipsSeen, k)
	}

	ok := lastOk != nil
	hasLoopback := false
	for _, ip := range ipsSeen {
		if ip == "127.0.0.1" || ip == "::1" {
			hasLoopback = true
			break
		}
	}
	msg := "No tracker announces recorded for this torrent. Main server may not be seeding or not announcing to this tracker."
	if ok {
		msg = "Tracker has received at least one successful announce."
		if hasLoopback {
			msg = "Tracker is receiving announces but from 127.0.0.1. Set OMNICLOUD_TRACKER_ANNOUNCE_HOST to the main server's public IP or hostname so downloaders can connect."
		}
	}

	respondJSON(w, http.StatusOK, TorrentPeerStatus{
		InfoHash:         infoHash,
		SeedersCount:     seedersCount,
		AnnounceAttempts: announceAttempts,
		LastOkAnnounceAt: lastOk,
		IPsSeen:          ipsSeen,
		Ok:               ok,
		Message:          msg,
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

	query := `
		SELECT COALESCE(info_hash, ''), COALESCE(peer_id, ''), COALESCE(ip, ''), COALESCE(port, 0),
		       COALESCE(event, ''), status, COALESCE(failure_reason, ''), created_at
		FROM torrent_announce_attempts
		WHERE info_hash = $1
		ORDER BY created_at DESC
		LIMIT $2
	`
	rows, err := s.db.Query(query, infoHash, limit)
	if err != nil {
		log.Printf("List announce attempts query failed (info_hash=%s): %v", infoHash, err)
		respondError(w, http.StatusInternalServerError, "Failed to query announce attempts", "")
		return
	}
	defer rows.Close()

	var out []AnnounceAttemptInfo
	for rows.Next() {
		var item AnnounceAttemptInfo
		if err := rows.Scan(
			&item.InfoHash,
			&item.PeerID,
			&item.IP,
			&item.Port,
			&item.Event,
			&item.Status,
			&item.FailureReason,
			&item.CreatedAt,
		); err != nil {
			log.Printf("List announce attempts scan failed: %v", err)
			respondError(w, http.StatusInternalServerError, "Failed to scan announce attempt", "")
			return
		}
		out = append(out, item)
	}

	respondJSON(w, http.StatusOK, out)
}

// handleTrackerLive returns tracker-wide live state with active swarm peers and known torrent rollup.
func (s *Server) handleTrackerLive(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.trackerHandler.(interface {
		GetSnapshot() torrent.TrackerSnapshot
	})
	if !ok || provider == nil {
		respondJSON(w, http.StatusOK, TrackerLiveResponse{
			TrackerAvailable: false,
			GeneratedAt:      time.Now(),
			ActiveSwarmPeers: []torrent.SwarmSnapshot{},
			Torrents:         []TrackerLiveTorrent{},
		})
		return
	}

	snap := provider.GetSnapshot()
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
		SELECT t.id, t.package_id, COALESCE(dp.package_name, ''), COALESCE(dp.content_title, ''), t.info_hash, t.created_at
		FROM dcp_torrents t
		LEFT JOIN dcp_packages dp ON dp.id = t.package_id
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
		if scanErr := rows.Scan(
			&item.ID,
			&item.PackageID,
			&item.PackageName,
			&item.ContentTitle,
			&item.InfoHash,
			&item.CreatedAt,
		); scanErr != nil {
			respondError(w, http.StatusInternalServerError, "Failed to scan torrent", "")
			return
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
		       dt.package_id, dp.package_name,
		       COALESCE(dst_srv.name, '') AS destination_server_name,
		       COALESCE(src_srv.name, '') AS source_server_name,
		       COALESCE(dt.total_size_bytes, dp.total_size_bytes, 0) AS total_size_bytes
		FROM transfers t
		LEFT JOIN dcp_torrents dt ON t.torrent_id = dt.id
		LEFT JOIN dcp_packages dp ON dt.package_id = dp.id
		LEFT JOIN servers dst_srv ON t.destination_server_id::text = dst_srv.id::text
		LEFT JOIN servers src_srv ON t.source_server_id::text = src_srv.id::text
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
		var destServerName, srcServerName string
		var totalSizeBytes int64
		var etaSeconds sql.NullInt64
		var startedAt, completedAt sql.NullTime

		err := rows.Scan(&ti.ID, &ti.TorrentID, &sourceServerID, &ti.DestinationServerID, &ti.RequestedBy,
			&ti.Status, &ti.Priority, &ti.ProgressPercent, &ti.DownloadedBytes,
			&ti.DownloadSpeedBps, &ti.UploadSpeedBps, &ti.PeersConnected, &etaSeconds,
			&errorMsg, &startedAt, &completedAt, &ti.CreatedAt, &ti.UpdatedAt,
			&packageID, &packageName, &destServerName, &srcServerName, &totalSizeBytes)
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
		ti.DestinationServerName = destServerName
		ti.SourceServerName = srcServerName
		ti.TotalSizeBytes = totalSizeBytes

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
	deleteData := r.URL.Query().Get("delete_data") == "true"

	// Set status to cancelled and queue a cancel command for the client
	query := `UPDATE transfers SET status = 'cancelled', delete_data = $1, pending_command = 'cancel', command_acknowledged = false, updated_at = $2 WHERE id = $3`
	_, err := s.db.Exec(query, deleteData, time.Now(), transferID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to cancel transfer", "")
		return
	}

	action := "cancelled (data kept)"
	if deleteData {
		action = "cancelled (data will be deleted)"
	}
	log.Printf("[transfers] Transfer %s %s", transferID, action)

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Transfer " + action,
	})
}

// handlePauseTransfer pauses an active transfer — sends pause command to client
func (s *Server) handlePauseTransfer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	transferID := vars["id"]

	query := `UPDATE transfers SET status = 'paused', pending_command = 'pause', command_acknowledged = false, updated_at = $1
	           WHERE id = $2 AND status IN ('downloading', 'checking', 'queued')`
	result, err := s.db.Exec(query, time.Now(), transferID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to pause transfer", "")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondError(w, http.StatusConflict, "Transfer not in a pausable state", "")
		return
	}
	log.Printf("[transfers] Transfer %s paused", transferID)
	respondJSON(w, http.StatusOK, map[string]string{"message": "Transfer paused"})
}

// handleResumeTransfer resumes a paused transfer — sends resume command to client
func (s *Server) handleResumeTransfer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	transferID := vars["id"]

	query := `UPDATE transfers SET status = 'downloading', pending_command = 'resume', command_acknowledged = false, updated_at = $1
	           WHERE id = $2 AND status = 'paused'`
	result, err := s.db.Exec(query, time.Now(), transferID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to resume transfer", "")
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondError(w, http.StatusConflict, "Transfer not paused", "")
		return
	}
	log.Printf("[transfers] Transfer %s resumed", transferID)
	respondJSON(w, http.StatusOK, map[string]string{"message": "Transfer resumed"})
}

// handleRetryTransfer resets a failed/errored transfer back to queued so it can be retried
func (s *Server) handleRetryTransfer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	transferID := vars["id"]

	query := `
		UPDATE transfers SET
			status = 'queued',
			error_message = NULL,
			progress_percent = 0,
			downloaded_bytes = 0,
			download_speed_bps = 0,
			upload_speed_bps = 0,
			peers_connected = 0,
			eta_seconds = NULL,
			started_at = NULL,
			completed_at = NULL,
			updated_at = $1
		WHERE id = $2
		AND status IN ('error', 'failed', 'cancelled')
	`
	result, err := s.db.Exec(query, time.Now(), transferID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to retry transfer", err.Error())
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		respondError(w, http.StatusBadRequest, "Transfer cannot be retried", "Only error, failed, or cancelled transfers can be retried")
		return
	}

	log.Printf("Transfer %s retried (reset to queued)", transferID)
	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Transfer queued for retry",
	})
}

// TorrentQueueItem represents an item in the torrent generation queue
type TorrentQueueItem struct {
	ID              string     `json:"id"`
	PackageID       string     `json:"package_id"`
	PackageName     string     `json:"package_name"`
	ServerID        string     `json:"server_id"`
	ServerName      string     `json:"server_name"`
	Status          string     `json:"status"`
	ProgressPercent float64    `json:"progress_percent"`
	CurrentFile     *string    `json:"current_file"`
	ErrorMessage    *string    `json:"error_message"`
	QueuePosition   int        `json:"queue_position"`
	QueuedAt        time.Time  `json:"queued_at"`
	StartedAt       *time.Time `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at"`
	ETASeconds      *int       `json:"eta_seconds"`
	HashingSpeedBps *int64     `json:"hashing_speed_bps,omitempty"`
	TotalSizeBytes  *int64     `json:"total_size_bytes,omitempty"`
	BytesHashed     *int64     `json:"bytes_hashed,omitempty"`
	TotalPieces     *int       `json:"total_pieces,omitempty"`
	PiecesHashed    *int       `json:"pieces_hashed,omitempty"`
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
			COALESCE(NULLIF(TRIM(dp.content_title), ''), NULLIF(TRIM(dp.package_name), ''), '(Unknown package)') as package_name,
			tq.server_id,
			COALESCE(NULLIF(TRIM(srv.name), ''), 'Unknown Server') as server_name,
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

		// #region agent log
		if f, e := os.OpenFile("/home/appbox/DCPCLOUDAPP/.cursor/debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); e == nil {
			payload, _ := json.Marshal(map[string]interface{}{"location": "torrent_handlers.go:handleListTorrentQueue", "message": "queue item", "data": map[string]interface{}{"server_id": item.ServerID, "server_name": item.ServerName, "status": item.Status, "progress_percent": item.ProgressPercent, "package_id": item.PackageID}, "hypothesisId": "H1", "timestamp": time.Now().Unix() * 1000})
			f.Write(append(payload, '\n'))
			f.Close()
		}
		// #endregion

		// Fix display when server or package has no name (unknown location / no DCP details)
		if item.ServerName == "" || item.ServerName == "Unknown Server" {
			// Backfill server name in DB so future requests show a name
			if _, err := s.db.Exec(`UPDATE servers SET name = COALESCE(NULLIF(TRIM(name), ''), 'Server ' || LEFT(id::text, 8)) WHERE id = $1 AND (name IS NULL OR TRIM(name) = '')`, item.ServerID); err == nil {
				// Re-fetch name for this response
				var name string
				if s.db.QueryRow(`SELECT COALESCE(NULLIF(TRIM(name), ''), 'Server ' || LEFT(id::text, 8)) FROM servers WHERE id = $1`, item.ServerID).Scan(&name) == nil {
					item.ServerName = name
				} else {
					item.ServerName = "Server " + item.ServerID
					if len(item.ServerID) > 8 {
						item.ServerName = "Server " + item.ServerID[:8]
					}
				}
			} else {
				item.ServerName = "Server " + item.ServerID
				if len(item.ServerID) > 8 {
					item.ServerName = "Server " + item.ServerID[:8]
				}
			}
		}
		if item.PackageName == "" || item.PackageName == "(Unknown package)" {
			item.PackageName = "Package " + item.PackageID
			if len(item.PackageID) > 8 {
				item.PackageName = "Package " + item.PackageID[:8]
			}
		}

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

		// Include total size
		if totalSizeBytes.Valid && totalSizeBytes.Int64 > 0 {
			item.TotalSizeBytes = &totalSizeBytes.Int64

			// Calculate bytes hashed from progress and total size
			bytesHashed := int64(item.ProgressPercent / 100.0 * float64(totalSizeBytes.Int64))
			item.BytesHashed = &bytesHashed

			// Estimate total pieces and pieces hashed (16MB piece size for <100GB, 32MB for larger)
			var pieceSizeBytes int64 = 16 * 1024 * 1024
			if totalSizeBytes.Int64 >= 100*1024*1024*1024 {
				pieceSizeBytes = 32 * 1024 * 1024
			}
			totalPieces := int(totalSizeBytes.Int64/pieceSizeBytes) + 1
			piecesHashed := int(float64(totalPieces) * item.ProgressPercent / 100.0)
			item.TotalPieces = &totalPieces
			item.PiecesHashed = &piecesHashed
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

// HashCheckRequest is the body for checking whether this server should hash a package
type HashCheckRequest struct {
	AssetMapUUID string `json:"assetmap_uuid"`
}

// HashCheckResponse tells the client whether to hash, wait, or download
type HashCheckResponse struct {
	Action        string  `json:"action"`          // "hash", "wait", "download"
	HashingServer string  `json:"hashing_server,omitempty"` // server name if waiting
	Progress      float64 `json:"progress,omitempty"`       // hashing progress if waiting
}

// handleHashCheck checks whether a client server should hash a package or if another server is already handling it.
// Returns: "hash" (go ahead), "wait" (another server is hashing), "download" (torrent exists already).
func (s *Server) handleHashCheck(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var req HashCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", "")
		return
	}

	assetMapUUID, err := uuid.Parse(req.AssetMapUUID)
	if err != nil || req.AssetMapUUID == "" {
		respondError(w, http.StatusBadRequest, "Invalid assetmap_uuid", "")
		return
	}

	// Resolve assetmap_uuid to package_id on the main server
	var packageID uuid.UUID
	err = s.db.QueryRow("SELECT id FROM dcp_packages WHERE assetmap_uuid = $1", assetMapUUID).Scan(&packageID)
	if err == sql.ErrNoRows {
		// Package not yet synced to main server — let the client hash it
		respondJSON(w, http.StatusOK, HashCheckResponse{Action: "hash"})
		return
	} else if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query package", "")
		return
	}

	// 1. Check if torrent already exists for this package
	var torrentExists bool
	err = s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM dcp_torrents WHERE package_id = $1)", packageID).Scan(&torrentExists)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to check torrent", "")
		return
	}
	if torrentExists {
		respondJSON(w, http.StatusOK, HashCheckResponse{Action: "download"})
		return
	}

	// 2. Check if any OTHER server is actively GENERATING (hashing) this package.
	// Only block if the other server has actually started hashing (status='generating'),
	// not just queued. First server to start generating wins the race.
	var hashingServerName string
	var progress float64
	err = s.db.QueryRow(`
		SELECT COALESCE(srv.name, ''), COALESCE(tq.progress_percent, 0)
		FROM torrent_queue tq
		LEFT JOIN servers srv ON srv.id = tq.server_id
		WHERE tq.package_id = $1
		  AND tq.server_id != $2
		  AND tq.status = 'generating'
		LIMIT 1
	`, packageID, serverID).Scan(&hashingServerName, &progress)

	if err == nil {
		// Another server is actively hashing this package
		respondJSON(w, http.StatusOK, HashCheckResponse{
			Action:        "wait",
			HashingServer: hashingServerName,
			Progress:      progress,
		})
		return
	}
	// err == sql.ErrNoRows means no other server is actively hashing → proceed

	// 3. No torrent, no other server generating → grant hash permission
	respondJSON(w, http.StatusOK, HashCheckResponse{Action: "hash"})
}

// ClaimTorrentQueueRequest is the body for claiming a package for torrent generation
type ClaimTorrentQueueRequest struct {
	PackageID string `json:"package_id"`
}

// handleClaimTorrentQueue lets a client claim the right to generate a torrent for a package.
// Returns 409 if another server already has this package queued or generating (only one generator per package).
func (s *Server) handleClaimTorrentQueue(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	var req ClaimTorrentQueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", "")
		return
	}
	packageID, err := uuid.Parse(req.PackageID)
	if err != nil || req.PackageID == "" {
		respondError(w, http.StatusBadRequest, "Invalid package_id", "")
		return
	}

	// Use same atomic claim as main server so only one server can generate per package
	claimed, err := torrent.ClaimGeneration(s.db, packageID.String(), serverID.String(), "")
	if err != nil {
		log.Printf("Error claiming queue: %v", err)
		respondError(w, http.StatusInternalServerError, "Failed to claim", "")
		return
	}
	if !claimed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "another server is already generating this package"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "claimed"})
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
		serverID string
		status   string
		queuedAt time.Time
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

// handleCancelQueueItem cancels a generating or queued torrent hash
func (s *Server) handleCancelQueueItem(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	queueItemID := vars["id"]

	// Get queue item details before cancelling
	var packageID, serverID, status string
	err := s.db.QueryRow(`
		SELECT package_id, server_id, status
		FROM torrent_queue
		WHERE id = $1
	`, queueItemID).Scan(&packageID, &serverID, &status)

	if err == sql.ErrNoRows {
		respondError(w, http.StatusNotFound, "Queue item not found", "")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query queue item", "")
		return
	}

	// Only allow cancelling queued or generating items
	if status != "queued" && status != "generating" {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Cannot cancel item in status '%s'", status), "Only queued or generating items can be cancelled")
		return
	}

	// Update queue item to cancelled
	query := `
		UPDATE torrent_queue
		SET status = 'cancelled',
		    cancelled_by = 'user',
		    completed_at = $1,
		    error_message = 'Cancelled by user'
		WHERE id = $2
	`

	result, err := s.db.Exec(query, time.Now(), queueItemID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to cancel queue item", "")
		return
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		respondError(w, http.StatusNotFound, "Queue item not found", "")
		return
	}

	// Release the claim so another server can try if needed
	_, err = s.db.Exec(`
		DELETE FROM torrent_generation_claim
		WHERE package_id = $1 AND server_id = $2
	`, packageID, serverID)
	if err != nil {
		log.Printf("Warning: failed to release claim for cancelled item: %v", err)
	}

	// Clean up any checkpoints
	_, err = s.db.Exec(`
		DELETE FROM torrent_generation_checkpoints
		WHERE package_id = $1 AND server_id = $2
	`, packageID, serverID)
	if err != nil {
		log.Printf("Warning: failed to clean up checkpoints for cancelled item: %v", err)
	}

	log.Printf("Cancelled queue item %s (package=%s, server=%s)", queueItemID, packageID, serverID)

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Queue item cancelled successfully",
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

// PackageServerStatusItem represents per-server content status for a DCP package
type PackageServerStatusItem struct {
	ServerID          string  `json:"server_id"`
	ServerName        string  `json:"server_name"`
	Status            string  `json:"status"` // seeding, downloading, paused, checking, incomplete, error, complete, missing
	ProgressPercent   float64 `json:"progress_percent"`
	DownloadedBytes   int64   `json:"downloaded_bytes"`
	TotalSizeBytes    int64   `json:"total_size_bytes"`
	DownloadSpeedBps  int64   `json:"download_speed_bps"`
	PeersConnected    int     `json:"peers_connected"`
	ETASeconds        *int    `json:"eta_seconds"`
	ErrorMessage      *string `json:"error_message"`
	TransferID        *string `json:"transfer_id"`
	HasLocalData      bool    `json:"has_local_data"`
	IsSeeding         bool    `json:"is_seeding"`
	LastSeen          *string `json:"last_seen"`
	IngestionStatus   string  `json:"ingestion_status,omitempty"`
	RosettaBridgePath string  `json:"rosettabridge_path,omitempty"`
	DownloadPath      string  `json:"download_path,omitempty"`
}

// handleGetPackageServerStatus returns per-server content status for a package.
// Aggregates: server_dcp_inventory, transfers, torrent_seeders, server_torrent_stats.
func (s *Server) handleGetPackageServerStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	packageID := vars["id"]

	// Get all servers
	serverRows, err := s.db.Query("SELECT id, name, last_seen FROM servers ORDER BY name")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to query servers", "")
		return
	}
	defer serverRows.Close()

	type serverInfo struct {
		id       string
		name     string
		lastSeen sql.NullTime
	}
	var allServers []serverInfo
	for serverRows.Next() {
		var si serverInfo
		if err := serverRows.Scan(&si.id, &si.name, &si.lastSeen); err != nil {
			continue
		}
		allServers = append(allServers, si)
	}

	// Get servers that have this package in inventory
	inventorySet := make(map[string]bool)
	invRows, err := s.db.Query("SELECT server_id FROM server_dcp_inventory WHERE package_id = $1 AND status = 'online'", packageID)
	if err == nil {
		defer invRows.Close()
		for invRows.Next() {
			var sid string
			if err := invRows.Scan(&sid); err == nil {
				inventorySet[sid] = true
			}
		}
	}

	// Get torrent info for this package
	var torrentID, infoHash string
	var torrentTotalSize int64
	err = s.db.QueryRow(`
		SELECT id, info_hash, total_size_bytes FROM dcp_torrents WHERE package_id = $1
	`, packageID).Scan(&torrentID, &infoHash, &torrentTotalSize)
	hasTorrent := err == nil

	// Get seeders for this torrent
	seederMap := make(map[string]struct {
		status       string
		lastAnnounce time.Time
	})
	if hasTorrent {
		seederRows, err := s.db.Query(`
			SELECT server_id, status, last_announce FROM torrent_seeders WHERE torrent_id = $1
		`, torrentID)
		if err == nil {
			defer seederRows.Close()
			for seederRows.Next() {
				var sid, status string
				var lastAnnounce time.Time
				if err := seederRows.Scan(&sid, &status, &lastAnnounce); err == nil {
					seederMap[sid] = struct {
						status       string
						lastAnnounce time.Time
					}{status, lastAnnounce}
				}
			}
		}
	}

	// Get active transfers for this torrent, keyed by destination_server_id
	type transferInfo struct {
		id              string
		status          string
		progressPercent float64
		downloadedBytes int64
		downloadSpeed   int64
		peersConnected  int
		etaSeconds      sql.NullInt64
		errorMessage    sql.NullString
		totalSizeBytes  int64
	}
	transferMap := make(map[string]transferInfo)
	if hasTorrent {
		tRows, err := s.db.Query(`
			SELECT t.id, t.destination_server_id, t.status, t.progress_percent, t.downloaded_bytes,
			       t.download_speed_bps, t.peers_connected, t.eta_seconds, t.error_message,
			       COALESCE(dp.total_size_bytes, 0)
			FROM transfers t
			LEFT JOIN dcp_torrents dt ON dt.id = t.torrent_id
			LEFT JOIN dcp_packages dp ON dp.id = dt.package_id
			WHERE t.torrent_id = $1
			ORDER BY t.created_at DESC
		`, torrentID)
		if err == nil {
			defer tRows.Close()
			for tRows.Next() {
				var ti transferInfo
				var destServerID string
				if err := tRows.Scan(&ti.id, &destServerID, &ti.status, &ti.progressPercent,
					&ti.downloadedBytes, &ti.downloadSpeed, &ti.peersConnected,
					&ti.etaSeconds, &ti.errorMessage, &ti.totalSizeBytes); err == nil {
					// Only keep the most recent transfer per server
					if _, exists := transferMap[destServerID]; !exists {
						transferMap[destServerID] = ti
					}
				}
			}
		}
	}

	// Get ingestion status for this package (all servers)
	type ingestionInfo struct {
		status           string
		downloadPath     string
		rosettabridgePath string
	}
	ingestionMap := make(map[string]ingestionInfo)
	ingRows, err := s.db.Query(`
		SELECT server_id, status, download_path, COALESCE(rosettabridge_path, '')
		FROM dcp_ingestion_status WHERE package_id = $1
	`, packageID)
	if err == nil {
		defer ingRows.Close()
		for ingRows.Next() {
			var sid, st, dlPath, rbPath string
			if err := ingRows.Scan(&sid, &st, &dlPath, &rbPath); err == nil {
				ingestionMap[sid] = ingestionInfo{status: st, downloadPath: dlPath, rosettabridgePath: rbPath}
			}
		}
	}

	// Build per-server status
	items := make([]PackageServerStatusItem, 0, len(allServers))
	for _, srv := range allServers {
		item := PackageServerStatusItem{
			ServerID:     srv.id,
			ServerName:   srv.name,
			Status:       "missing",
			HasLocalData: inventorySet[srv.id],
		}
		if srv.lastSeen.Valid {
			ls := srv.lastSeen.Time.Format(time.RFC3339)
			item.LastSeen = &ls
		}

		// Check seeder status
		if seeder, ok := seederMap[srv.id]; ok {
			if seeder.status == "seeding" || seeder.status == "completed" {
				item.IsSeeding = true
				item.Status = "seeding"
				item.ProgressPercent = 100
				item.DownloadedBytes = torrentTotalSize
				item.TotalSizeBytes = torrentTotalSize
			}
		}

		// Check transfer status (overrides seeding for active transfers)
		if ti, ok := transferMap[srv.id]; ok {
			tid := ti.id
			item.TransferID = &tid
			item.ProgressPercent = ti.progressPercent
			item.DownloadedBytes = ti.downloadedBytes
			item.TotalSizeBytes = ti.totalSizeBytes
			item.DownloadSpeedBps = ti.downloadSpeed
			item.PeersConnected = ti.peersConnected
			if ti.etaSeconds.Valid {
				eta := int(ti.etaSeconds.Int64)
				item.ETASeconds = &eta
			}
			if ti.errorMessage.Valid && ti.errorMessage.String != "" {
				item.ErrorMessage = &ti.errorMessage.String
			}

			switch ti.status {
			case "downloading":
				item.Status = "downloading"
			case "paused":
				item.Status = "paused"
			case "checking":
				item.Status = "checking"
			case "queued":
				item.Status = "queued"
			case "error", "failed":
				item.Status = "error"
			case "completed":
				// Transfer completed — check if seeding
				if item.IsSeeding {
					item.Status = "seeding"
				} else {
					item.Status = "complete"
				}
				item.ProgressPercent = 100
			case "cancelled":
				// Cancelled — check if data still on disk
				if item.HasLocalData {
					item.Status = "incomplete"
				} else {
					item.Status = "missing"
					item.TransferID = nil
					item.ProgressPercent = 0
					item.DownloadedBytes = 0
					item.TotalSizeBytes = 0
					item.DownloadSpeedBps = 0
					item.PeersConnected = 0
					item.ETASeconds = nil
				}
			}
		} else if item.Status == "missing" && item.HasLocalData {
			// Has inventory entry but no transfer and not seeding
			item.Status = "complete"
			item.ProgressPercent = 100
			item.TotalSizeBytes = torrentTotalSize
			item.DownloadedBytes = torrentTotalSize
		}

		// Add ingestion status if available
		if ing, ok := ingestionMap[srv.id]; ok {
			item.IngestionStatus = ing.status
			item.RosettaBridgePath = ing.rosettabridgePath
			item.DownloadPath = ing.downloadPath
		}

		items = append(items, item)
	}

	respondJSON(w, http.StatusOK, items)
}

// handleCreateContentCommand creates a content management command (delete) for a server
func (s *Server) handleCreateContentCommand(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PackageID  string `json:"package_id"`
		ServerID   string `json:"server_id"`
		Command    string `json:"command"`     // "delete"
		TargetPath string `json:"target_path"` // optional: specific path to delete from
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", "")
		return
	}
	if req.PackageID == "" || req.ServerID == "" || req.Command == "" {
		respondError(w, http.StatusBadRequest, "Missing required fields", "")
		return
	}
	if req.Command != "delete" {
		respondError(w, http.StatusBadRequest, "Only 'delete' command is supported", "")
		return
	}

	// Get package name and info hash
	var packageName string
	var infoHash sql.NullString
	err := s.db.QueryRow(`
		SELECT dp.package_name, dt.info_hash
		FROM dcp_packages dp
		LEFT JOIN dcp_torrents dt ON dt.package_id = dp.id
		WHERE dp.id = $1
	`, req.PackageID).Scan(&packageName, &infoHash)
	if err != nil {
		respondError(w, http.StatusNotFound, "Package not found", "")
		return
	}

	ih := ""
	if infoHash.Valid {
		ih = infoHash.String
	}

	// Check for existing active transfer — if one exists, cancel it with delete_data
	var existingTransferID string
	err = s.db.QueryRow(`
		SELECT t.id FROM transfers t
		JOIN dcp_torrents dt ON dt.id = t.torrent_id
		WHERE dt.package_id = $1 AND t.destination_server_id = $2
		AND t.status IN ('downloading', 'paused', 'checking', 'queued', 'error', 'failed')
		ORDER BY t.created_at DESC LIMIT 1
	`, req.PackageID, req.ServerID).Scan(&existingTransferID)

	if err == nil && existingTransferID != "" {
		// Cancel the existing transfer with delete_data
		_, err = s.db.Exec(`
			UPDATE transfers SET status = 'cancelled', delete_data = true,
			pending_command = 'cancel', command_acknowledged = false, updated_at = $1
			WHERE id = $2
		`, time.Now(), existingTransferID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to cancel transfer", "")
			return
		}
		log.Printf("[content-cmd] Cancelled transfer %s with delete for package %s on server %s", existingTransferID, packageName, req.ServerID)
		respondJSON(w, http.StatusOK, map[string]string{"message": "Transfer cancelled with delete", "transfer_id": existingTransferID})
		return
	}

	// No active transfer — create a content command
	_, err = s.db.Exec(`
		INSERT INTO content_commands (package_id, server_id, package_name, info_hash, command, target_path)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, req.PackageID, req.ServerID, packageName, ih, req.Command, req.TargetPath)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create command", "")
		return
	}

	log.Printf("[content-cmd] Created delete command for package %s on server %s", packageName, req.ServerID)
	respondJSON(w, http.StatusOK, map[string]string{"message": "Delete command queued"})
}
