-- Apply as catalog_svc, like every migration here:
--   PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -f <this file>
--
-- Applied as the superuser the tables are owned by that user and the service
-- gets "permission denied" on every read that touches them. See 0014_saved.sql,
-- which is where that was learned.

-- Playlists, per member.
--
-- Which side of the schema this falls on was the first question asked about it.
-- videos and channels are the household's; watch_progress, reactions,
-- subscriptions, watch_later and saved are each keyed by user_id. A playlist is
-- a collection somebody assembled, so it belongs with the second group — and
-- importing one member's YouTube playlists into a shared table would repeat
-- exactly the defect 0014 was written to fix.
--
-- Sharing later is a column on this table; splitting a shared table apart
-- afterwards is a migration nobody wants to write.
CREATE TABLE IF NOT EXISTS playlists (
  id          text PRIMARY KEY,
  user_id     text NOT NULL,
  title       text NOT NULL,
  description text NOT NULL DEFAULT '',
  -- The YouTube playlist this was imported from, NULL when it was made here.
  -- Kept so a re-import updates the same list rather than making another.
  source_url  text,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS playlists_user_idx ON playlists (user_id, created_at DESC);

-- One imported playlist per member per source. Partial, so any number of
-- locally made lists can share the absence of a source.
CREATE UNIQUE INDEX IF NOT EXISTS playlists_user_source_idx
  ON playlists (user_id, source_url) WHERE source_url IS NOT NULL;

-- position is filled from the order the import returned, and appended to at the
-- end when somebody adds a video here. A playlist of music that comes back
-- shuffled is broken, so the order is carried rather than derived from a date.
-- Reordering by hand has no column of its own to add later: this is that column.
CREATE TABLE IF NOT EXISTS playlist_items (
  playlist_id text    NOT NULL REFERENCES playlists (id) ON DELETE CASCADE,
  video_id    text    NOT NULL REFERENCES videos (id)    ON DELETE CASCADE,
  position    integer NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (playlist_id, video_id)
);

CREATE INDEX IF NOT EXISTS playlist_items_order_idx
  ON playlist_items (playlist_id, position);
