-- Per-server, per-torrent status (seeding, error) so the UI can show why a server is not seeding.
CREATE TABLE IF NOT EXISTS server_torrent_status (
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    torrent_id UUID NOT NULL REFERENCES dcp_torrents(id) ON DELETE CASCADE,
    status VARCHAR(50) NOT NULL DEFAULT 'pending',
    error_message TEXT,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (server_id, torrent_id)
);

CREATE INDEX IF NOT EXISTS idx_server_torrent_status_torrent_id ON server_torrent_status(torrent_id);
CREATE INDEX IF NOT EXISTS idx_server_torrent_status_server_id ON server_torrent_status(server_id);
