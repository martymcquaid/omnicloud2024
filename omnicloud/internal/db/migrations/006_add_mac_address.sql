-- Add MAC address field to servers table for authentication
ALTER TABLE servers ADD COLUMN IF NOT EXISTS mac_address VARCHAR(17);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS registration_key_hash VARCHAR(255);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS is_authorized BOOLEAN DEFAULT false;

-- Create index for MAC address lookups
CREATE INDEX IF NOT EXISTS idx_servers_mac_address ON servers(mac_address);
CREATE INDEX IF NOT EXISTS idx_servers_is_authorized ON servers(is_authorized);
