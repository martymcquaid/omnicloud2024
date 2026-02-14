-- Add unique constraints for metadata sync from clients
-- This allows upserts to work correctly

-- Add unique constraint on dcp_compositions (package_id, cpl_uuid)
ALTER TABLE dcp_compositions DROP CONSTRAINT IF EXISTS dcp_compositions_package_id_cpl_uuid_key;
ALTER TABLE dcp_compositions ADD CONSTRAINT dcp_compositions_package_id_cpl_uuid_key UNIQUE (package_id, cpl_uuid);

-- Add unique constraint on dcp_assets (package_id, asset_uuid)
ALTER TABLE dcp_assets DROP CONSTRAINT IF EXISTS dcp_assets_package_id_asset_uuid_key;
ALTER TABLE dcp_assets ADD CONSTRAINT dcp_assets_package_id_asset_uuid_key UNIQUE (package_id, asset_uuid);
