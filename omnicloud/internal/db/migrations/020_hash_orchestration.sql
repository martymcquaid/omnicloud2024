-- Migration 020: Hash Orchestration Improvements
-- Add cancellation tracking and formalize claim system

-- Add cancellation tracking to torrent_queue
ALTER TABLE torrent_queue ADD COLUMN IF NOT EXISTS cancelled_by TEXT;
-- Values: 'user' = manual cancel, 'system' = auto-cancel, 'claim_lost' = another server claimed

-- Ensure torrent_generation_claim table exists with proper constraints
CREATE TABLE IF NOT EXISTS torrent_generation_claim (
    package_id UUID PRIMARY KEY REFERENCES dcp_packages(id) ON DELETE CASCADE,
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    queue_item_id UUID REFERENCES torrent_queue(id) ON DELETE SET NULL,
    claimed_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
);

-- Add indexes for faster lookups
CREATE INDEX IF NOT EXISTS idx_claim_server ON torrent_generation_claim(server_id);
CREATE INDEX IF NOT EXISTS idx_claim_time ON torrent_generation_claim(claimed_at);
CREATE INDEX IF NOT EXISTS idx_torrents_package ON dcp_torrents(package_id);
CREATE INDEX IF NOT EXISTS idx_queue_status ON torrent_queue(status, server_id);

-- Add function to clean up stale claims (claims >3 hours old with no progress)
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

-- Optional: Automatically clean up stale claims when checking queue
-- (Note: actual cleanup logic will be in Go code for better control)
