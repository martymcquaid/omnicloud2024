-- Rescan command and last scan status (main server stores per-server for UI)
ALTER TABLE servers ADD COLUMN IF NOT EXISTS rescan_requested_at TIMESTAMP WITH TIME ZONE;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS last_scan_result JSONB;
CREATE INDEX IF NOT EXISTS idx_servers_rescan_requested_at ON servers(rescan_requested_at) WHERE rescan_requested_at IS NOT NULL;
