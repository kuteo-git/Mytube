-- Apply as catalog_svc:
--   PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -f <this file>

-- A playlist YouTube lists but will not hand over.
--
-- Measured on this installation: of 27 playlists on /feed/playlists, 10 answer
-- "YouTube said: The playlist does not exist" when asked for by URL — with a
-- live session, reproducibly, minutes apart. They are listed and they are not
-- readable, and nothing about the listing says which.
--
-- Without somewhere to record that, each one costs a request every pass, for
-- ever, and sits at the front of the unread queue blocking playlists that could
-- have been read. It is the same shape as ingest.unavailable_sources — "can this
-- be fetched" is a question about upstream, asked once and remembered — but
-- about a playlist rather than a video, so it lives with the playlist.
--
-- NULL/false means "no reason to think otherwise", which is what every existing
-- row is. Only upstream's own refusal sets it.
ALTER TABLE playlists ADD COLUMN IF NOT EXISTS unavailable boolean NOT NULL DEFAULT false;

-- The unread queue skips them, so the index it uses has to as well.
DROP INDEX IF EXISTS playlists_items_synced_idx;
CREATE INDEX IF NOT EXISTS playlists_items_synced_idx
  ON playlists (items_synced_at NULLS FIRST)
  WHERE source_url IS NOT NULL AND NOT unavailable;
