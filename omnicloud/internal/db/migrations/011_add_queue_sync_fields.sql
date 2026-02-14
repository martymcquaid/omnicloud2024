-- Add fields to track synced queue items from remote servers
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS synced_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS hashing_speed_bps BIGINT DEFAULT 0;

-- Add index for synced_at to help with monitoring stale syncs
CREATE INDEX IF NOT EXISTS idx_torrent_queue_synced_at ON torrent_queue(synced_at);
