-- Add display_name to servers: user-defined label, never overwritten by client sync.
-- Client-reported hostname stays in name; UI shows display_name when set, else name.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS display_name VARCHAR(255);
