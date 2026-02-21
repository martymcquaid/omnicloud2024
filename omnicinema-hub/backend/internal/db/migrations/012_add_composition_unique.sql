-- Add unique constraint on dcp_compositions (package_id, cpl_uuid) so ON CONFLICT can be used
-- and InsertDCPComposition can RETURNING id for existing rows (reels then get valid composition_id).
ALTER TABLE dcp_compositions DROP CONSTRAINT IF EXISTS dcp_compositions_package_id_cpl_uuid_key;
ALTER TABLE dcp_compositions ADD CONSTRAINT dcp_compositions_package_id_cpl_uuid_key UNIQUE (package_id, cpl_uuid);
