-- Imported YouTube comments have no local user. Local comments always provide user_id,
-- so queries that depend on it are unaffected.
ALTER TABLE catalog.comments ALTER COLUMN user_id DROP NOT NULL;
