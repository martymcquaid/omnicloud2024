-- Create table for storing intermediate piece hashes during torrent generation
-- This enables resuming hash generation after restarts instead of starting from scratch

CREATE TABLE IF NOT EXISTS torrent_generation_checkpoints (
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    piece_index INTEGER NOT NULL,
    piece_hash BYTEA NOT NULL,  -- 20 bytes (SHA1 hash)
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (package_id, server_id, piece_index)
);

-- Index for efficient lookup when loading checkpoints on resume
CREATE INDEX IF NOT EXISTS idx_torrent_gen_checkpoints_lookup
    ON torrent_generation_checkpoints(package_id, server_id);

-- Optional: Add tracking fields to torrent_queue for monitoring checkpoint usage
ALTER TABLE torrent_queue
    ADD COLUMN IF NOT EXISTS checkpoint_pieces INTEGER DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_checkpoint_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resumed_from_piece INTEGER;

COMMENT ON TABLE torrent_generation_checkpoints IS 'Stores intermediate piece hashes during torrent generation to enable resume after restart';
COMMENT ON COLUMN torrent_generation_checkpoints.piece_hash IS 'SHA1 hash of the piece (20 bytes)';
COMMENT ON COLUMN torrent_queue.checkpoint_pieces IS 'Number of checkpointed pieces for this generation job';
COMMENT ON COLUMN torrent_queue.last_checkpoint_at IS 'Timestamp of the last checkpoint write';
COMMENT ON COLUMN torrent_queue.resumed_from_piece IS 'If resumed, the piece index it resumed from (null if fresh start)';
