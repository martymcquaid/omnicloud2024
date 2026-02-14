-- Add software version tracking
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

-- Add version tracking to servers
ALTER TABLE servers ADD COLUMN IF NOT EXISTS software_version VARCHAR(50);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS last_version_check TIMESTAMP;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS target_version VARCHAR(50);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS upgrade_status VARCHAR(20) DEFAULT 'idle';
-- upgrade_status can be: idle, pending, upgrading, success, failed

-- Create index for version lookups
CREATE INDEX IF NOT EXISTS idx_software_versions_version ON software_versions(version);
CREATE INDEX IF NOT EXISTS idx_servers_software_version ON servers(software_version);
