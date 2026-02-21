package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// ServerTorrentStatsResponse represents detailed torrent stats for a server
type ServerTorrentStatsResponse struct {
	ServerID            string  `json:"server_id"`
	ServerName          string  `json:"server_name"`
	InfoHash            string  `json:"info_hash"`
	PackageID           string  `json:"package_id"`
	PackageName         string  `json:"package_name"`
	ContentTitle        string  `json:"content_title,omitempty"`
	Status              string  `json:"status"`
	IsLoaded            bool    `json:"is_loaded"`
	IsSeeding           bool    `json:"is_seeding"`
	IsDownloading       bool    `json:"is_downloading"`
	BytesCompleted      int64   `json:"bytes_completed"`
	BytesTotal          int64   `json:"bytes_total"`
	ProgressPercent     float64 `json:"progress_percent"`
	PiecesCompleted     int     `json:"pieces_completed"`
	PiecesTotal         int     `json:"pieces_total"`
	DownloadSpeedBps    int64   `json:"download_speed_bps"`
	UploadSpeedBps      int64   `json:"upload_speed_bps"`
	UploadedBytes       int64   `json:"uploaded_bytes"`
	PeersConnected      int     `json:"peers_connected"`
	ETASeconds          *int    `json:"eta_seconds,omitempty"`
	AnnouncedToTracker  bool    `json:"announced_to_tracker"`
	LastAnnounceAttempt *string `json:"last_announce_attempt,omitempty"`
	LastAnnounceSuccess *string `json:"last_announce_success,omitempty"`
	AnnounceError       *string `json:"announce_error,omitempty"`
	UpdatedAt           string  `json:"updated_at"`
}

// handleGetServerTorrentStats returns detailed torrent stats for a specific server
func (s *Server) handleGetServerTorrentStats(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	serverID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "Invalid server ID", err.Error())
		return
	}

	query := `
		SELECT
			sts.server_id,
			COALESCE(NULLIF(TRIM(srv.display_name), ''), srv.name, '') as server_name,
			sts.info_hash,
			COALESCE(dt.package_id::text, '') as package_id,
			COALESCE(dp.package_name, '') as package_name,
			COALESCE(dp.content_title, '') as content_title,
			sts.status,
			sts.is_loaded,
			sts.is_seeding,
			sts.is_downloading,
			sts.bytes_completed,
			sts.bytes_total,
			sts.progress_percent,
			sts.pieces_completed,
			sts.pieces_total,
			sts.download_speed_bps,
			sts.upload_speed_bps,
			sts.uploaded_bytes,
			sts.peers_connected,
			sts.eta_seconds,
			sts.announced_to_tracker,
			sts.last_announce_attempt,
			sts.last_announce_success,
			sts.announce_error,
			sts.updated_at
		FROM server_torrent_stats sts
		LEFT JOIN servers srv ON srv.id = sts.server_id
		LEFT JOIN dcp_torrents dt ON dt.info_hash = sts.info_hash
		LEFT JOIN dcp_packages dp ON dp.id = dt.package_id
		WHERE sts.server_id = $1
		ORDER BY dp.package_name, sts.info_hash
	`

	rows, err := s.db.Query(query, serverID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database query failed", err.Error())
		return
	}
	defer rows.Close()

	stats := []ServerTorrentStatsResponse{}
	for rows.Next() {
		var stat ServerTorrentStatsResponse
		var etaSeconds sql.NullInt32
		var lastAnnounceAttempt, lastAnnounceSuccess sql.NullTime
		var announceError sql.NullString

		err := rows.Scan(
			&stat.ServerID,
			&stat.ServerName,
			&stat.InfoHash,
			&stat.PackageID,
			&stat.PackageName,
			&stat.ContentTitle,
			&stat.Status,
			&stat.IsLoaded,
			&stat.IsSeeding,
			&stat.IsDownloading,
			&stat.BytesCompleted,
			&stat.BytesTotal,
			&stat.ProgressPercent,
			&stat.PiecesCompleted,
			&stat.PiecesTotal,
			&stat.DownloadSpeedBps,
			&stat.UploadSpeedBps,
			&stat.UploadedBytes,
			&stat.PeersConnected,
			&etaSeconds,
			&stat.AnnouncedToTracker,
			&lastAnnounceAttempt,
			&lastAnnounceSuccess,
			&announceError,
			&stat.UpdatedAt,
		)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Scan error", err.Error())
			return
		}

		// Handle nullable fields
		if etaSeconds.Valid {
			eta := int(etaSeconds.Int32)
			stat.ETASeconds = &eta
		}
		if lastAnnounceAttempt.Valid {
			t := lastAnnounceAttempt.Time.Format("2006-01-02T15:04:05Z07:00")
			stat.LastAnnounceAttempt = &t
		}
		if lastAnnounceSuccess.Valid {
			t := lastAnnounceSuccess.Time.Format("2006-01-02T15:04:05Z07:00")
			stat.LastAnnounceSuccess = &t
		}
		if announceError.Valid && announceError.String != "" {
			stat.AnnounceError = &announceError.String
		}

		stats = append(stats, stat)
	}

	if err := rows.Err(); err != nil {
		respondError(w, http.StatusInternalServerError, "Rows iteration error", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleGetAllServersTorrentStats returns torrent stats for all servers (for overview page)
func (s *Server) handleGetAllServersTorrentStats(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT
			sts.server_id,
			COALESCE(NULLIF(TRIM(srv.display_name), ''), srv.name, '') as server_name,
			sts.info_hash,
			COALESCE(dt.package_id::text, '') as package_id,
			COALESCE(dp.package_name, '') as package_name,
			COALESCE(dp.content_title, '') as content_title,
			sts.status,
			sts.is_loaded,
			sts.is_seeding,
			sts.is_downloading,
			sts.bytes_completed,
			sts.bytes_total,
			sts.progress_percent,
			sts.pieces_completed,
			sts.pieces_total,
			sts.download_speed_bps,
			sts.upload_speed_bps,
			sts.uploaded_bytes,
			sts.peers_connected,
			sts.eta_seconds,
			sts.announced_to_tracker,
			sts.last_announce_attempt,
			sts.last_announce_success,
			sts.announce_error,
			sts.updated_at
		FROM server_torrent_stats sts
		LEFT JOIN servers srv ON srv.id = sts.server_id
		LEFT JOIN dcp_torrents dt ON dt.info_hash = sts.info_hash
		LEFT JOIN dcp_packages dp ON dp.id = dt.package_id
		ORDER BY COALESCE(NULLIF(TRIM(srv.display_name), ''), srv.name), dp.package_name, sts.info_hash
	`

	rows, err := s.db.Query(query)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Database query failed", err.Error())
		return
	}
	defer rows.Close()

	stats := []ServerTorrentStatsResponse{}
	for rows.Next() {
		var stat ServerTorrentStatsResponse
		var etaSeconds sql.NullInt32
		var lastAnnounceAttempt, lastAnnounceSuccess sql.NullTime
		var announceError sql.NullString

		err := rows.Scan(
			&stat.ServerID,
			&stat.ServerName,
			&stat.InfoHash,
			&stat.PackageID,
			&stat.PackageName,
			&stat.ContentTitle,
			&stat.Status,
			&stat.IsLoaded,
			&stat.IsSeeding,
			&stat.IsDownloading,
			&stat.BytesCompleted,
			&stat.BytesTotal,
			&stat.ProgressPercent,
			&stat.PiecesCompleted,
			&stat.PiecesTotal,
			&stat.DownloadSpeedBps,
			&stat.UploadSpeedBps,
			&stat.UploadedBytes,
			&stat.PeersConnected,
			&etaSeconds,
			&stat.AnnouncedToTracker,
			&lastAnnounceAttempt,
			&lastAnnounceSuccess,
			&announceError,
			&stat.UpdatedAt,
		)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Scan error", err.Error())
			return
		}

		// Handle nullable fields
		if etaSeconds.Valid {
			eta := int(etaSeconds.Int32)
			stat.ETASeconds = &eta
		}
		if lastAnnounceAttempt.Valid {
			t := lastAnnounceAttempt.Time.Format("2006-01-02T15:04:05Z07:00")
			stat.LastAnnounceAttempt = &t
		}
		if lastAnnounceSuccess.Valid {
			t := lastAnnounceSuccess.Time.Format("2006-01-02T15:04:05Z07:00")
			stat.LastAnnounceSuccess = &t
		}
		if announceError.Valid && announceError.String != "" {
			stat.AnnounceError = &announceError.String
		}

		stats = append(stats, stat)
	}

	if err := rows.Err(); err != nil {
		respondError(w, http.StatusInternalServerError, "Rows iteration error", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
