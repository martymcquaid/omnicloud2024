CREATE TABLE IF NOT EXISTS torrent_announce_attempts (
    id BIGSERIAL PRIMARY KEY,
    info_hash VARCHAR(40),
    peer_id TEXT,
    ip TEXT,
    port INTEGER,
    event TEXT,
    status TEXT NOT NULL,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_torrent_announce_attempts_hash_created
    ON torrent_announce_attempts (info_hash, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_torrent_announce_attempts_created
    ON torrent_announce_attempts (created_at DESC);
