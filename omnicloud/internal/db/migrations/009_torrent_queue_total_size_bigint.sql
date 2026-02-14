-- Ensure total_size_bytes is BIGINT (handles DCPs > 2GB); no-op if already BIGINT
ALTER TABLE torrent_queue ALTER COLUMN total_size_bytes TYPE BIGINT USING total_size_bytes::bigint;
