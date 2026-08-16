-- Apply as catalog_svc:
--   PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -f <this file>

-- When this playlist's contents were last read from YouTube.
--
-- Separate from updated_at, which moves whenever anybody edits the playlist
-- here. The importer needs "which of these have I not looked at in longest",
-- and updated_at answers a different question — a playlist somebody added a
-- video to this morning would look freshly synced.
--
-- NULL means never, and those are taken first: a playlist imported by name with
-- no contents yet is an empty page with a title, which is worse than not having
-- imported it.
ALTER TABLE playlists ADD COLUMN IF NOT EXISTS items_synced_at timestamptz;

-- The importer's own order: stalest first, never-synced before that.
CREATE INDEX IF NOT EXISTS playlists_items_synced_idx
  ON playlists (items_synced_at NULLS FIRST)
  WHERE source_url IS NOT NULL;
