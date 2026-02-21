-- Create server_dcp_inventory table (junction: which servers have which DCPs)
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

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_server_dcp_inventory_server_id ON server_dcp_inventory(server_id);
CREATE INDEX IF NOT EXISTS idx_server_dcp_inventory_package_id ON server_dcp_inventory(package_id);
CREATE INDEX IF NOT EXISTS idx_server_dcp_inventory_status ON server_dcp_inventory(status);
CREATE INDEX IF NOT EXISTS idx_server_dcp_inventory_last_verified ON server_dcp_inventory(last_verified);

-- Trigger for updated_at
CREATE TRIGGER update_server_dcp_inventory_updated_at BEFORE UPDATE ON server_dcp_inventory
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Create scan_logs table for audit trail of scans
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

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_scan_logs_server_id ON scan_logs(server_id);
CREATE INDEX IF NOT EXISTS idx_scan_logs_scan_type ON scan_logs(scan_type);
CREATE INDEX IF NOT EXISTS idx_scan_logs_started_at ON scan_logs(started_at);
CREATE INDEX IF NOT EXISTS idx_scan_logs_status ON scan_logs(status);
