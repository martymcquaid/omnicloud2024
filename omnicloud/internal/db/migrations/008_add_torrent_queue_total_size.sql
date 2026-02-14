-- Add total_size_bytes to torrent_queue for speed calculation during generation
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS total_size_bytes BIGINT;
