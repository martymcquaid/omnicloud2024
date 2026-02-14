-- Create dcp_packages table for main DCP package records
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

-- Create indexes for common queries
CREATE INDEX IF NOT EXISTS idx_dcp_packages_assetmap_uuid ON dcp_packages(assetmap_uuid);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_package_name ON dcp_packages(package_name);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_content_title ON dcp_packages(content_title);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_content_kind ON dcp_packages(content_kind);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_issuer ON dcp_packages(issuer);
CREATE INDEX IF NOT EXISTS idx_dcp_packages_discovered_at ON dcp_packages(discovered_at);

-- Trigger for updated_at
CREATE TRIGGER update_dcp_packages_updated_at BEFORE UPDATE ON dcp_packages
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
