-- Create dcp_assets table for MXF and other assets from PKL
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

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_dcp_assets_package_id ON dcp_assets(package_id);
CREATE INDEX IF NOT EXISTS idx_dcp_assets_asset_uuid ON dcp_assets(asset_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_assets_asset_role ON dcp_assets(asset_role);
CREATE INDEX IF NOT EXISTS idx_dcp_assets_file_name ON dcp_assets(file_name);

-- Create dcp_packing_lists table for PKL metadata
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

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_dcp_packing_lists_package_id ON dcp_packing_lists(package_id);
CREATE INDEX IF NOT EXISTS idx_dcp_packing_lists_pkl_uuid ON dcp_packing_lists(pkl_uuid);
