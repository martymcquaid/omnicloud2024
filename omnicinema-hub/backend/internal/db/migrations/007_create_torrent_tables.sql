-- Create dcp_torrents table for torrent file registry
CREATE TABLE IF NOT EXISTS dcp_torrents (
    id UUID PRIMARY KEY,
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    info_hash VARCHAR(40) NOT NULL UNIQUE,
    torrent_file BYTEA NOT NULL,
    piece_size INTEGER NOT NULL,
    total_pieces INTEGER NOT NULL,
    created_by_server_id UUID NOT NULL REFERENCES servers(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    file_count INTEGER,
    total_size_bytes BIGINT
);

CREATE INDEX IF NOT EXISTS idx_dcp_torrents_package_id ON dcp_torrents(package_id);
CREATE INDEX IF NOT EXISTS idx_dcp_torrents_info_hash ON dcp_torrents(info_hash);
CREATE INDEX IF NOT EXISTS idx_dcp_torrents_created_by ON dcp_torrents(created_by_server_id);

-- Create torrent_seeders table for tracking which servers are seeding
CREATE TABLE IF NOT EXISTS torrent_seeders (
    id UUID PRIMARY KEY,
    torrent_id UUID NOT NULL REFERENCES dcp_torrents(id) ON DELETE CASCADE,
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    local_path VARCHAR(1024) NOT NULL,
    status VARCHAR(50) DEFAULT 'seeding',
    uploaded_bytes BIGINT DEFAULT 0,
    last_announce TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(torrent_id, server_id)
);

CREATE INDEX IF NOT EXISTS idx_torrent_seeders_torrent_id ON torrent_seeders(torrent_id);
CREATE INDEX IF NOT EXISTS idx_torrent_seeders_server_id ON torrent_seeders(server_id);
CREATE INDEX IF NOT EXISTS idx_torrent_seeders_status ON torrent_seeders(status);

CREATE TRIGGER update_torrent_seeders_updated_at BEFORE UPDATE ON torrent_seeders
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create transfers table for managing DCP transfers between sites
CREATE TABLE IF NOT EXISTS transfers (
    id UUID PRIMARY KEY,
    torrent_id UUID NOT NULL REFERENCES dcp_torrents(id) ON DELETE CASCADE,
    source_server_id UUID REFERENCES servers(id),
    destination_server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    requested_by VARCHAR(255),
    status VARCHAR(50) DEFAULT 'queued',
    priority INTEGER DEFAULT 5,
    progress_percent DECIMAL(5,2) DEFAULT 0,
    downloaded_bytes BIGINT DEFAULT 0,
    download_speed_bps BIGINT DEFAULT 0,
    upload_speed_bps BIGINT DEFAULT 0,
    peers_connected INTEGER DEFAULT 0,
    eta_seconds INTEGER,
    error_message TEXT,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_transfers_torrent_id ON transfers(torrent_id);
CREATE INDEX IF NOT EXISTS idx_transfers_destination_server_id ON transfers(destination_server_id);
CREATE INDEX IF NOT EXISTS idx_transfers_status ON transfers(status);
CREATE INDEX IF NOT EXISTS idx_transfers_priority ON transfers(priority);
CREATE INDEX IF NOT EXISTS idx_transfers_created_at ON transfers(created_at);

CREATE TRIGGER update_transfers_updated_at BEFORE UPDATE ON transfers
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create torrent_queue table for managing torrent generation queue
CREATE TABLE IF NOT EXISTS torrent_queue (
    id UUID PRIMARY KEY,
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    status VARCHAR(50) DEFAULT 'queued',
    progress_percent DECIMAL(5,2) DEFAULT 0,
    current_file VARCHAR(512),
    error_message TEXT,
    queued_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    started_at TIMESTAMP WITH TIME ZONE,
    completed_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(package_id, server_id)
);

CREATE INDEX IF NOT EXISTS idx_torrent_queue_package_id ON torrent_queue(package_id);
CREATE INDEX IF NOT EXISTS idx_torrent_queue_server_id ON torrent_queue(server_id);
CREATE INDEX IF NOT EXISTS idx_torrent_queue_status ON torrent_queue(status);
CREATE INDEX IF NOT EXISTS idx_torrent_queue_queued_at ON torrent_queue(queued_at);
