package main

// Embedded migration SQL - compiled into the binary so migrations work on
// deployed servers that don't have the source tree on disk.

var embeddedMigrationSQL = map[string]string{

	"000_enable_extensions": `
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
`,

	"001_create_servers": `
CREATE TABLE IF NOT EXISTS servers (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    location VARCHAR(255),
    api_url VARCHAR(512),
    last_seen TIMESTAMP WITH TIME ZONE,
    storage_capacity_tb DECIMAL(10, 2),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_servers_name ON servers(name);
CREATE INDEX IF NOT EXISTS idx_servers_last_seen ON servers(last_seen);
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';
DO $$ BEGIN
    CREATE TRIGGER update_servers_updated_at BEFORE UPDATE ON servers
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
`,

	"002_create_dcp_packages": `
CREATE TABLE IF NOT EXISTS dcp_packages (
    id UUID PRIMARY KEY,
    assetmap_uuid UUID NOT NULL UNIQUE,
    package_name VARCHAR(512) NOT NULL,
    content_title VARCHAR(512),
    content_kind VARCHAR(50),
    issue_date TIMESTAMP WITH TIME ZONE,
    issuer VARCHAR(255),
    creator VARCHAR(255),
    annotation_text TEXT,
    volume_count INTEGER DEFAULT 1,
    total_size_bytes BIGINT,
    file_count INTEGER,
    discovered_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    last_verified TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_assetmap_uuid ON dcp_packages(assetmap_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_package_name ON dcp_packages(package_name);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_content_title ON dcp_packages(content_title);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_content_kind ON dcp_packages(content_kind);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_issuer ON dcp_packages(issuer);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_discovered_at ON dcp_packages(discovered_at);
DO $$ BEGIN
    CREATE TRIGGER update_dcp_packages_updated_at BEFORE UPDATE ON dcp_packages
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
`,

	"003_create_dcp_compositions": `
CREATE TABLE IF NOT EXISTS dcp_compositions (
    id UUID PRIMARY KEY,
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    cpl_uuid UUID NOT NULL,
    content_title_text VARCHAR(512),
    full_content_title VARCHAR(512),
    content_kind VARCHAR(50),
    content_version_id UUID,
    label_text VARCHAR(255),
    issue_date TIMESTAMP WITH TIME ZONE,
    issuer VARCHAR(255),
    creator VARCHAR(255),
    edit_rate VARCHAR(50),
    frame_rate VARCHAR(50),
    screen_aspect_ratio VARCHAR(50),
    resolution_width INTEGER,
    resolution_height INTEGER,
    main_sound_configuration VARCHAR(255),
    main_sound_sample_rate VARCHAR(50),
    luminance INTEGER,
    release_territory VARCHAR(10),
    distributor VARCHAR(255),
    facility VARCHAR(255),
    reel_count INTEGER,
    total_duration_frames INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_package_id ON dcp_compositions(package_id);
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_cpl_uuid ON dcp_compositions(cpl_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_content_kind ON dcp_compositions(content_kind);
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_release_territory ON dcp_compositions(release_territory);
CREATE INDEX IF NOT EXISTS idx_dcp_compositions_resolution ON dcp_compositions(resolution_width, resolution_height);
DO $$ BEGIN
    CREATE TRIGGER update_dcp_compositions_updated_at BEFORE UPDATE ON dcp_compositions
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
CREATE TABLE IF NOT EXISTS dcp_reels (
    id UUID PRIMARY KEY,
    composition_id UUID NOT NULL REFERENCES dcp_compositions(id) ON DELETE CASCADE,
    reel_uuid UUID NOT NULL,
    reel_number INTEGER NOT NULL,
    duration_frames INTEGER,
    picture_asset_uuid UUID,
    picture_edit_rate VARCHAR(50),
    picture_entry_point INTEGER,
    picture_intrinsic_duration INTEGER,
    picture_key_id UUID,
    picture_hash VARCHAR(255),
    sound_asset_uuid UUID,
    sound_configuration VARCHAR(255),
    subtitle_asset_uuid UUID,
    subtitle_language VARCHAR(10),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_dcp_reels_composition_id ON dcp_reels(composition_id);
CREATE INDEX IF NOT EXISTS idx_dcp_reels_reel_uuid ON dcp_reels(reel_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_reels_reel_number ON dcp_reels(reel_number);
CREATE INDEX IF NOT EXISTS idx_dcp_reels_picture_asset_uuid ON dcp_reels(picture_asset_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_reels_sound_asset_uuid ON dcp_reels(sound_asset_uuid);
`,

	"004_create_dcp_assets": `
CREATE TABLE IF NOT EXISTS dcp_assets (
    id UUID PRIMARY KEY,
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    asset_uuid UUID NOT NULL,
    file_path VARCHAR(1024),
    file_name VARCHAR(512),
    asset_type VARCHAR(100),
    asset_role VARCHAR(50),
    size_bytes BIGINT,
    hash_algorithm VARCHAR(50) DEFAULT 'SHA1',
    hash_value VARCHAR(255),
    chunk_offset BIGINT DEFAULT 0,
    chunk_length BIGINT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_dcp_assets_package_id ON dcp_assets(package_id);
CREATE INDEX IF NOT EXISTS idx_dcp_assets_asset_uuid ON dcp_assets(asset_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_assets_asset_role ON dcp_assets(asset_role);
CREATE INDEX IF NOT EXISTS idx_dcp_assets_file_name ON dcp_assets(file_name);
CREATE TABLE IF NOT EXISTS dcp_packing_lists (
    id UUID PRIMARY KEY,
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    pkl_uuid UUID NOT NULL,
    annotation_text TEXT,
    issue_date TIMESTAMP WITH TIME ZONE,
    issuer VARCHAR(255),
    creator VARCHAR(255),
    asset_count INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_dcp_packing_lists_package_id ON dcp_packing_lists(package_id);
CREATE INDEX IF NOT EXISTS idx_dcp_packing_lists_pkl_uuid ON dcp_packing_lists(pkl_uuid);
`,

	"005_create_inventory": `
CREATE TABLE IF NOT EXISTS server_dcp_inventory (
    id UUID PRIMARY KEY,
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    local_path VARCHAR(1024) NOT NULL,
    status VARCHAR(50) DEFAULT 'online',
    last_verified TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(server_id, package_id)
);
CREATE INDEX IF NOT EXISTS idx_server_dcp_inventory_server_id ON server_dcp_inventory(server_id);
CREATE INDEX IF NOT EXISTS idx_server_dcp_inventory_package_id ON server_dcp_inventory(package_id);
CREATE INDEX IF NOT EXISTS idx_server_dcp_inventory_status ON server_dcp_inventory(status);
CREATE INDEX IF NOT EXISTS idx_server_dcp_inventory_last_verified ON server_dcp_inventory(last_verified);
DO $$ BEGIN
    CREATE TRIGGER update_server_dcp_inventory_updated_at BEFORE UPDATE ON server_dcp_inventory
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
CREATE TABLE IF NOT EXISTS scan_logs (
    id UUID PRIMARY KEY,
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    scan_type VARCHAR(50) NOT NULL,
    started_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP WITH TIME ZONE,
    packages_found INTEGER DEFAULT 0,
    packages_added INTEGER DEFAULT 0,
    packages_updated INTEGER DEFAULT 0,
    packages_removed INTEGER DEFAULT 0,
    errors TEXT,
    status VARCHAR(50) DEFAULT 'running'
);
CREATE INDEX IF NOT EXISTS idx_scan_logs_server_id ON scan_logs(server_id);
CREATE INDEX IF NOT EXISTS idx_scan_logs_scan_type ON scan_logs(scan_type);
CREATE INDEX IF NOT EXISTS idx_scan_logs_started_at ON scan_logs(started_at);
CREATE INDEX IF NOT EXISTS idx_scan_logs_status ON scan_logs(status);
`,

	"006_add_mac_address": `
ALTER TABLE servers ADD COLUMN IF NOT EXISTS mac_address VARCHAR(17);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS registration_key_hash VARCHAR(255);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS is_authorized BOOLEAN DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_servers_mac_address ON servers(mac_address);
CREATE INDEX IF NOT EXISTS idx_servers_is_authorized ON servers(is_authorized);
`,

	"007_create_torrent_tables": `
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
DO $$ BEGIN
    CREATE TRIGGER update_torrent_seeders_updated_at BEFORE UPDATE ON torrent_seeders
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
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
DO $$ BEGIN
    CREATE TRIGGER update_transfers_updated_at BEFORE UPDATE ON transfers
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
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
`,

	"008_add_torrent_queue_total_size": `
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS total_size_bytes BIGINT;
`,

	"009_torrent_queue_total_size_bigint": `
ALTER TABLE torrent_queue ALTER COLUMN total_size_bytes TYPE BIGINT USING total_size_bytes::bigint;
`,

	"010_add_software_versions": `
CREATE TABLE IF NOT EXISTS software_versions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    version VARCHAR(50) NOT NULL UNIQUE,
    build_time TIMESTAMP NOT NULL,
    checksum VARCHAR(64) NOT NULL,
    size_bytes BIGINT NOT NULL,
    download_url TEXT NOT NULL,
    is_stable BOOLEAN DEFAULT true,
    release_notes TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS software_version VARCHAR(50);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS last_version_check TIMESTAMP;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS target_version VARCHAR(50);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS upgrade_status VARCHAR(20) DEFAULT 'idle';
CREATE INDEX IF NOT EXISTS idx_software_versions_version ON software_versions(version);
CREATE INDEX IF NOT EXISTS idx_servers_software_version ON servers(software_version);
`,

	"011_add_queue_sync_fields": `
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS synced_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS hashing_speed_bps BIGINT DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_torrent_queue_synced_at ON torrent_queue(synced_at);
`,

	"012_add_composition_unique": `
ALTER TABLE dcp_compositions DROP CONSTRAINT IF EXISTS dcp_compositions_package_id_cpl_uuid_key;
ALTER TABLE dcp_compositions ADD CONSTRAINT dcp_compositions_package_id_cpl_uuid_key UNIQUE (package_id, cpl_uuid);
`,

	"012_add_metadata_sync_constraints": `
ALTER TABLE dcp_compositions DROP CONSTRAINT IF EXISTS dcp_compositions_package_id_cpl_uuid_key;
ALTER TABLE dcp_compositions ADD CONSTRAINT dcp_compositions_package_id_cpl_uuid_key UNIQUE (package_id, cpl_uuid);
ALTER TABLE dcp_assets DROP CONSTRAINT IF EXISTS dcp_assets_package_id_asset_uuid_key;
ALTER TABLE dcp_assets ADD CONSTRAINT dcp_assets_package_id_asset_uuid_key UNIQUE (package_id, asset_uuid);
`,

	"014_server_torrent_status": `
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
`,

	"015_create_torrent_announce_attempts": `
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
`,

	"016_torrent_queue_sync_columns": `
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS total_size_bytes BIGINT DEFAULT 0;
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS hashing_speed_bps BIGINT DEFAULT 0;
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS synced_at TIMESTAMP WITH TIME ZONE;
`,

	"017_server_torrent_stats": `
CREATE TABLE IF NOT EXISTS server_torrent_stats (
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    info_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    is_loaded BOOLEAN DEFAULT false,
    is_seeding BOOLEAN DEFAULT false,
    is_downloading BOOLEAN DEFAULT false,
    bytes_completed BIGINT DEFAULT 0,
    bytes_total BIGINT DEFAULT 0,
    progress_percent REAL DEFAULT 0,
    pieces_completed INTEGER DEFAULT 0,
    pieces_total INTEGER DEFAULT 0,
    download_speed_bps BIGINT DEFAULT 0,
    upload_speed_bps BIGINT DEFAULT 0,
    uploaded_bytes BIGINT DEFAULT 0,
    peers_connected INTEGER DEFAULT 0,
    eta_seconds INTEGER,
    announced_to_tracker BOOLEAN DEFAULT false,
    last_announce_attempt TIMESTAMPTZ,
    last_announce_success TIMESTAMPTZ,
    announce_error TEXT,
    updated_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (server_id, info_hash)
);
CREATE INDEX IF NOT EXISTS idx_server_torrent_stats_info_hash ON server_torrent_stats(info_hash);
CREATE INDEX IF NOT EXISTS idx_server_torrent_stats_status ON server_torrent_stats(server_id, status);
`,

	"018_torrent_piece_completion": `
CREATE TABLE IF NOT EXISTS torrent_piece_completion (
    info_hash TEXT NOT NULL,
    piece_index INTEGER NOT NULL,
    completed BOOLEAN DEFAULT false,
    verified_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (info_hash, piece_index)
);
CREATE INDEX IF NOT EXISTS idx_torrent_piece_completion_info_hash
    ON torrent_piece_completion(info_hash);
CREATE INDEX IF NOT EXISTS idx_torrent_piece_completion_completed
    ON torrent_piece_completion(info_hash, completed)
    WHERE completed = true;
`,

	"019_torrent_generation_checkpoints": `
CREATE TABLE IF NOT EXISTS torrent_generation_checkpoints (
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    piece_index INTEGER NOT NULL,
    piece_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (package_id, server_id, piece_index)
);
CREATE INDEX IF NOT EXISTS idx_torrent_gen_checkpoints_lookup
    ON torrent_generation_checkpoints(package_id, server_id);
ALTER TABLE torrent_queue
    ADD COLUMN IF NOT EXISTS checkpoint_pieces INTEGER DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_checkpoint_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resumed_from_piece INTEGER;
`,

	"020_hash_orchestration": `
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS cancelled_by TEXT;
CREATE TABLE IF NOT EXISTS torrent_generation_claim (
    package_id UUID PRIMARY KEY REFERENCES dcp_packages(id) ON DELETE CASCADE,
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    queue_item_id UUID REFERENCES torrent_queue(id) ON DELETE SET NULL,
    claimed_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_claim_server ON torrent_generation_claim(server_id);
CREATE INDEX IF NOT EXISTS idx_claim_time ON torrent_generation_claim(claimed_at);
CREATE INDEX IF NOT EXISTS idx_torrents_package ON dcp_torrents(package_id);
CREATE INDEX IF NOT EXISTS idx_queue_status ON torrent_queue(status, server_id);
CREATE OR REPLACE FUNCTION cleanup_stale_claims() RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    DELETE FROM torrent_generation_claim
    WHERE claimed_at < NOW() - INTERVAL '3 hours'
    AND NOT EXISTS (
        SELECT 1 FROM torrent_queue
        WHERE torrent_queue.package_id = torrent_generation_claim.package_id
        AND torrent_queue.server_id = torrent_generation_claim.server_id
        AND torrent_queue.status = 'generating'
        AND torrent_queue.synced_at > NOW() - INTERVAL '10 minutes'
    );
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;
`,

	"021_transfer_commands": `
ALTER TABLE transfers ADD COLUMN IF NOT EXISTS delete_data BOOLEAN DEFAULT FALSE;
ALTER TABLE transfers ADD COLUMN IF NOT EXISTS pending_command TEXT DEFAULT '';
ALTER TABLE transfers ADD COLUMN IF NOT EXISTS command_acknowledged BOOLEAN DEFAULT TRUE;
`,

	"022_content_commands": `
CREATE TABLE IF NOT EXISTS content_commands (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    package_id UUID NOT NULL,
    server_id UUID NOT NULL,
    package_name TEXT NOT NULL DEFAULT '',
    info_hash TEXT NOT NULL DEFAULT '',
    command TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    result_message TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    acknowledged_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_content_commands_server ON content_commands(server_id, status);
`,

	"023_server_settings": `
-- Add server settings columns for configurable paths and locations
ALTER TABLE servers ADD COLUMN IF NOT EXISTS download_location TEXT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS torrent_download_location TEXT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS watch_folder TEXT;

-- Create server_library_locations table for multiple library paths per server
CREATE TABLE IF NOT EXISTS server_library_locations (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    path TEXT NOT NULL,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_server_library_locations_server ON server_library_locations(server_id);
CREATE INDEX IF NOT EXISTS idx_server_library_locations_active ON server_library_locations(is_active);

DO $$ BEGIN
    CREATE TRIGGER update_server_library_locations_updated_at BEFORE UPDATE ON server_library_locations
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
`,

	"024_rosettabridge_support": `
-- Add location_type to library locations (default 'standard', can be 'rosettabridge')
ALTER TABLE server_library_locations ADD COLUMN IF NOT EXISTS location_type VARCHAR(50) DEFAULT 'standard';

-- Add auto_cleanup_after_ingestion setting to servers
ALTER TABLE servers ADD COLUMN IF NOT EXISTS auto_cleanup_after_ingestion BOOLEAN DEFAULT false;

-- Track ingestion status per server per package
CREATE TABLE IF NOT EXISTS dcp_ingestion_status (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    package_id UUID NOT NULL REFERENCES dcp_packages(id) ON DELETE CASCADE,
    info_hash TEXT NOT NULL DEFAULT '',
    download_path TEXT NOT NULL,
    rosettabridge_path TEXT DEFAULT '',
    status VARCHAR(50) DEFAULT 'downloaded',
    verified_at TIMESTAMP WITH TIME ZONE,
    switched_at TIMESTAMP WITH TIME ZONE,
    cleaned_at TIMESTAMP WITH TIME ZONE,
    error_message TEXT DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_dcp_ingestion_server ON dcp_ingestion_status(server_id);
CREATE INDEX IF NOT EXISTS idx_dcp_ingestion_status ON dcp_ingestion_status(status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_dcp_ingestion_unique ON dcp_ingestion_status(server_id, package_id);

DO $$ BEGIN
    CREATE TRIGGER update_dcp_ingestion_status_updated_at BEFORE UPDATE ON dcp_ingestion_status
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
`,

	"025_content_command_target_path": `
-- Add target_path to content_commands so clients can delete from specific locations
ALTER TABLE content_commands ADD COLUMN IF NOT EXISTS target_path TEXT DEFAULT '';
`,

	"026_reel_unique_constraint": `
-- Add unique constraint on (composition_id, reel_uuid) so reel upserts work correctly
ALTER TABLE dcp_reels DROP CONSTRAINT IF EXISTS dcp_reels_composition_id_reel_uuid_key;
ALTER TABLE dcp_reels ADD CONSTRAINT dcp_reels_composition_id_reel_uuid_key UNIQUE (composition_id, reel_uuid);
`,

	"027_nat_relay_status": `
-- NAT/relay status tracking for servers
ALTER TABLE servers ADD COLUMN IF NOT EXISTS is_behind_nat BOOLEAN DEFAULT false;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS relay_registered BOOLEAN DEFAULT false;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS nat_last_checked TIMESTAMP WITH TIME ZONE;
`,

	"028_server_display_name": `
-- User-defined server label; never overwritten by client sync (device-reported name stays in name)
ALTER TABLE servers ADD COLUMN IF NOT EXISTS display_name VARCHAR(255);
`,

	"029_create_users": `
-- User authentication tables
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    username VARCHAR(255) UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role VARCHAR(50) DEFAULT 'admin',
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
DO $$ BEGIN
    CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Session tokens for web UI authentication
CREATE TABLE IF NOT EXISTS user_sessions (
    token VARCHAR(128) PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_user_sessions_user_id ON user_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_sessions_expires_at ON user_sessions(expires_at);
`,

	"030_role_permissions": `
-- Role-based access control: maps roles to allowed pages
CREATE TABLE IF NOT EXISTS role_permissions (
    role VARCHAR(50) PRIMARY KEY,
    allowed_pages TEXT NOT NULL DEFAULT '[]',
    description VARCHAR(255) DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
DO $$ BEGIN
    CREATE TRIGGER update_role_permissions_updated_at BEFORE UPDATE ON role_permissions
        FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Seed default roles
INSERT INTO role_permissions (role, allowed_pages, description) VALUES
    ('admin', '["dashboard","dcps","servers","transfers","torrents","torrent-status","tracker","analytics","settings"]', 'Full access to all features')
ON CONFLICT (role) DO NOTHING;
INSERT INTO role_permissions (role, allowed_pages, description) VALUES
    ('it', '["dashboard","dcps","servers","transfers","torrents","torrent-status","tracker","analytics"]', 'Technical operations access')
ON CONFLICT (role) DO NOTHING;
INSERT INTO role_permissions (role, allowed_pages, description) VALUES
    ('manager', '["dashboard","dcps","servers","transfers","analytics"]', 'Content and transfer management')
ON CONFLICT (role) DO NOTHING;
`,

	"031_activity_logs": `
-- Activity/audit log: tracks all user actions for accountability
CREATE TABLE IF NOT EXISTS activity_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    username VARCHAR(255) NOT NULL DEFAULT '',
    action VARCHAR(100) NOT NULL,
    category VARCHAR(50) NOT NULL,
    resource_type VARCHAR(50) DEFAULT '',
    resource_id VARCHAR(255) DEFAULT '',
    resource_name VARCHAR(500) DEFAULT '',
    details TEXT DEFAULT '',
    ip_address VARCHAR(45) DEFAULT '',
    status VARCHAR(20) DEFAULT 'success',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_activity_logs_created_at ON activity_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_activity_logs_user_id ON activity_logs(user_id);
CREATE INDEX IF NOT EXISTS idx_activity_logs_category ON activity_logs(category);
CREATE INDEX IF NOT EXISTS idx_activity_logs_action ON activity_logs(action);
`,
}

// migrationOrder defines the execution order for migrations.
var migrationOrder = []string{
	"000_enable_extensions",
	"001_create_servers",
	"002_create_dcp_packages",
	"003_create_dcp_compositions",
	"004_create_dcp_assets",
	"005_create_inventory",
	"006_add_mac_address",
	"007_create_torrent_tables",
	"008_add_torrent_queue_total_size",
	"009_torrent_queue_total_size_bigint",
	"010_add_software_versions",
	"011_add_queue_sync_fields",
	"012_add_composition_unique",
	"012_add_metadata_sync_constraints",
	"014_server_torrent_status",
	"015_create_torrent_announce_attempts",
	"016_torrent_queue_sync_columns",
	"017_server_torrent_stats",
	"018_torrent_piece_completion",
	"019_torrent_generation_checkpoints",
	"020_hash_orchestration",
	"021_transfer_commands",
	"022_content_commands",
	"023_server_settings",
	"024_rosettabridge_support",
	"025_content_command_target_path",
	"026_reel_unique_constraint",
	"027_nat_relay_status",
	"028_server_display_name",
	"029_create_users",
	"030_role_permissions",
	"031_activity_logs",
}
