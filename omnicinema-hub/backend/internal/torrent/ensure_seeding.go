package torrent

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
)

// #region agent log
const ensureSeedingDebugLogPath = "/home/appbox/DCPCLOUDAPP/.cursor/debug.log"

func ensureSeedingDebugLog(location, message, hypothesisId string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	payload := map[string]interface{}{"location": location, "message": message, "hypothesisId": hypothesisId, "timestamp": time.Now().UnixNano() / 1e6, "data": data}
	f, _ := os.OpenFile(ensureSeedingDebugLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if f != nil {
		json.NewEncoder(f).Encode(payload)
		f.Close()
	}
}

// #endregion

// EnsureSeedingRestore runs once on startup (after a short delay) and optionally on an interval.
// For each package where this server has inventory and a torrent exists in dcp_torrents,
// starts seeding if not already active.
func EnsureSeedingRestore(ctx context.Context, db *sql.DB, client *Client, serverID string, interval time.Duration) {
	// Initial delay so DB and client are ready
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}
	run := func() {
		if err := ensureSeedingOnce(db, client, serverID); err != nil {
			log.Printf("Ensure seeding: %v", err)
		}
	}
	run()
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// upsertServerTorrentStatus records this server's status for a torrent (seeding or error) for UI display.
func upsertServerTorrentStatus(db *sql.DB, serverID, torrentID, status, errorMessage string) {
	query := `
		INSERT INTO server_torrent_status (server_id, torrent_id, status, error_message, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, NULLIF($4, ''), $5)
		ON CONFLICT (server_id, torrent_id)
		DO UPDATE SET status = $3, error_message = NULLIF($4, ''), updated_at = $5
	`
	now := time.Now()
	if _, err := db.Exec(query, serverID, torrentID, status, errorMessage, now); err != nil {
		log.Printf("Ensure seeding: failed to upsert server_torrent_status: %v", err)
	}
}

// ensureSeedingOnce queries packages that this server has in inventory and for which
// a torrent exists, then starts seeding each if not already active.
func ensureSeedingOnce(db *sql.DB, client *Client, serverID string) error {
	query := `
		SELECT dt.id, dt.package_id, dt.torrent_file, inv.local_path
		FROM server_dcp_inventory inv
		JOIN dcp_torrents dt ON dt.package_id = inv.package_id
		WHERE inv.server_id = $1 AND inv.local_path IS NOT NULL AND inv.local_path != ''
	`
	rows, err := db.Query(query, serverID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var inventoryRows int
	var restored int
	for rows.Next() {
		inventoryRows++
		var torrentID, packageID string
		var torrentFile []byte
		var localPath string
		if err := rows.Scan(&torrentID, &packageID, &torrentFile, &localPath); err != nil {
			log.Printf("Ensure seeding scan: %v", err)
			continue
		}
		if len(torrentFile) == 0 {
			continue
		}
		if err := client.StartSeeding(torrentFile, localPath, packageID, torrentID); err != nil {
			log.Printf("Ensure seeding start %s: %v", packageID, err)
			upsertServerTorrentStatus(db, serverID, torrentID, "error", err.Error())
			continue
		}
		upsertServerTorrentStatus(db, serverID, torrentID, "seeding", "")
		restored++
	}
	// #region agent log
	ensureSeedingDebugLog("ensure_seeding.go:ensureSeedingOnce", "restore run", "H_restore", map[string]interface{}{"inventory_rows": inventoryRows, "restored": restored, "server_id": serverID})
	// #endregion
	if restored > 0 {
		log.Printf("Ensure seeding: started %d torrent(s)", restored)
	}
	return nil
}

// SyncSeedersToDB periodically writes this server's current seeding state to torrent_seeders.
// This ensures the main server (which does not run the status reporter) still shows as seeding in the UI.
func SyncSeedersToDB(ctx context.Context, db *sql.DB, client *Client, serverID string, interval time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(10 * time.Second):
	}
	run := func() {
		if err := syncSeedersOnce(db, client, serverID); err != nil {
			log.Printf("Sync seeders to DB: %v", err)
		}
	}
	run()
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func syncSeedersOnce(db *sql.DB, client *Client, serverID string) error {
	list := client.GetSeedingTorrents()
	// #region agent log
	ensureSeedingDebugLog("ensure_seeding.go:syncSeedersOnce", "seeding from client", "H_sync", map[string]interface{}{"seeding_count_from_client": len(list), "server_id": serverID})
	// #endregion
	if len(list) == 0 {
		return nil
	}
	query := `
		INSERT INTO torrent_seeders (id, torrent_id, server_id, local_path, status, last_announce, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'seeding', $5, $5, $5)
		ON CONFLICT (torrent_id, server_id)
		DO UPDATE SET local_path = $4, status = 'seeding', last_announce = $5, updated_at = $5
	`
	now := time.Now()
	for _, item := range list {
		var torrentID string
		err := db.QueryRow("SELECT id FROM dcp_torrents WHERE info_hash = $1", item.InfoHash).Scan(&torrentID)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
		_, err = db.Exec(query, uuid.New().String(), torrentID, serverID, item.LocalPath, now)
		if err != nil {
			return err
		}
	}
	// #region agent log
	ensureSeedingDebugLog("ensure_seeding.go:syncSeedersOnce", "sync complete", "H_sync", map[string]interface{}{"upserted": len(list), "server_id": serverID})
	// #endregion
	return nil
}
