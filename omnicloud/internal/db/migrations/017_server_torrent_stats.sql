-- Add table for detailed per-server torrent stats (verification progress, piece info, etc.)
CREATE TABLE IF NOT EXISTS server_torrent_stats (
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    info_hash TEXT NOT NULL,

    -- Torrent client status
    status TEXT NOT NULL, -- 'verifying', 'seeding', 'downloading', 'paused', 'error'
    is_loaded BOOLEAN DEFAULT false, -- whether torrent is loaded in client
    is_seeding BOOLEAN DEFAULT false, -- whether actively seeding
    is_downloading BOOLEAN DEFAULT false, -- whether actively downloading

    -- Verification/download progress
    bytes_completed BIGINT DEFAULT 0,
    bytes_total BIGINT DEFAULT 0,
    progress_percent REAL DEFAULT 0,
    pieces_completed INTEGER DEFAULT 0,
    pieces_total INTEGER DEFAULT 0,

    -- Performance metrics
    download_speed_bps BIGINT DEFAULT 0,
    upload_speed_bps BIGINT DEFAULT 0,
    uploaded_bytes BIGINT DEFAULT 0,
    peers_connected INTEGER DEFAULT 0,
    eta_seconds INTEGER,

    -- Tracker status
    announced_to_tracker BOOLEAN DEFAULT false,
    last_announce_attempt TIMESTAMPTZ,
    last_announce_success TIMESTAMPTZ,
    announce_error TEXT,

    -- Timestamps
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (server_id, info_hash)
);

-- Index for querying by info_hash
CREATE INDEX IF NOT EXISTS idx_server_torrent_stats_info_hash ON server_torrent_stats(info_hash);

-- Index for querying by status
CREATE INDEX IF NOT EXISTS idx_server_torrent_stats_status ON server_torrent_stats(server_id, status);
