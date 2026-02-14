-- Create servers table to track all servers in the network
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

-- Create index for faster lookups
CREATE INDEX IF NOT EXISTS idx_servers_name ON servers(name);
CREATE INDEX IF NOT EXISTS idx_servers_last_seen ON servers(last_seen);

-- Create trigger to auto-update updated_at
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

CREATE TRIGGER update_servers_updated_at BEFORE UPDATE ON servers
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
