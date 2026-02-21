-- Store piece completion state in PostgreSQL instead of BoltDB
-- This avoids file locking issues and persists verification across restarts

CREATE TABLE IF NOT EXISTS torrent_piece_completion (
    info_hash TEXT NOT NULL,
    piece_index INTEGER NOT NULL,
    completed BOOLEAN DEFAULT false,
    verified_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (info_hash, piece_index)
);

-- Index for querying completion status of a torrent
CREATE INDEX IF NOT EXISTS idx_torrent_piece_completion_info_hash
    ON torrent_piece_completion(info_hash);

-- Index for counting completed pieces
CREATE INDEX IF NOT EXISTS idx_torrent_piece_completion_completed
    ON torrent_piece_completion(info_hash, completed)
    WHERE completed = true;

-- Comments
COMMENT ON TABLE torrent_piece_completion IS 'Stores piece verification state for torrents, replacing BoltDB to avoid file locking';
COMMENT ON COLUMN torrent_piece_completion.info_hash IS 'Torrent info hash (hex string)';
COMMENT ON COLUMN torrent_piece_completion.piece_index IS 'Zero-based piece index';
COMMENT ON COLUMN torrent_piece_completion.completed IS 'Whether this piece has been verified';
COMMENT ON COLUMN torrent_piece_completion.verified_at IS 'When piece was verified (for diagnostics)';
