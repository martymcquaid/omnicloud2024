-- Add columns for client-side hashing progress sync (main server merges from client reports)
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS total_size_bytes BIGINT DEFAULT 0;
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS hashing_speed_bps BIGINT DEFAULT 0;
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS synced_at TIMESTAMP WITH TIME ZONE;
