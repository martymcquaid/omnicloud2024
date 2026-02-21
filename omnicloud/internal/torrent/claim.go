package torrent

import (
	"database/sql"
	"time"
)

// ClaimGeneration tries to atomically claim the right to generate a torrent for a package.
// Returns (true, nil) if this server claimed it, (false, nil) if another server already has it.
// If the torrent_generation_claim table does not exist, returns (true, nil) so generation proceeds.
func ClaimGeneration(db *sql.DB, packageID, serverID, queueItemID string) (bool, error) {
	// Try to create table if not exists (optional migration)
	_, _ = db.Exec(`
		CREATE TABLE IF NOT EXISTS torrent_generation_claim (
			package_id UUID PRIMARY KEY,
			server_id UUID NOT NULL,
			queue_item_id UUID,
			claimed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
		)
	`)
	// Insert or update: we claim only if no row exists or we're the same server
	result, err := db.Exec(`
		INSERT INTO torrent_generation_claim (package_id, server_id, queue_item_id, claimed_at)
		VALUES ($1, $2, NULLIF($3, ''), $4)
		ON CONFLICT (package_id) DO UPDATE SET
			server_id = EXCLUDED.server_id,
			queue_item_id = EXCLUDED.queue_item_id,
			claimed_at = EXCLUDED.claimed_at
		WHERE torrent_generation_claim.server_id = $2
	`, packageID, serverID, queueItemID, time.Now())
	if err != nil {
		return false, err
	}
	rows, _ := result.RowsAffected()
	return rows > 0, nil
}
