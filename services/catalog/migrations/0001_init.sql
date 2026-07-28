-- Catalog schema. Owned by catalog_svc; no other service may read it.

SET search_path = catalog;

CREATE TABLE IF NOT EXISTS channels (
  id               text PRIMARY KEY,
  name             text        NOT NULL,
  handle           text        NOT NULL,
  avatar_path      text        NOT NULL DEFAULT '',
  subscriber_count bigint      NOT NULL DEFAULT 0,
  verified         boolean     NOT NULL DEFAULT false,
  created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS videos (
  id               text PRIMARY KEY,
  title            text        NOT NULL,
  channel_id       text        NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
  duration_seconds integer     NOT NULL,
  view_count       bigint      NOT NULL DEFAULT 0,
  published_at     timestamptz NOT NULL,
  added_at         timestamptz NOT NULL DEFAULT now(),
  thumbnail_path   text        NOT NULL DEFAULT '',
  description      text        NOT NULL DEFAULT '',
  hashtags         text[]      NOT NULL DEFAULT '{}',
  categories       text[]      NOT NULL DEFAULT '{}',
  -- Mirrors catalog.v1.MediaState.
  media_state      text        NOT NULL DEFAULT 'QUEUED',
  media_path       text        NOT NULL DEFAULT '',
  size_bytes       bigint      NOT NULL DEFAULT 0,
  pinned           boolean     NOT NULL DEFAULT false,
  source_url       text        NOT NULL,
  -- Drives LRU eviction. Touched on playback, not on grid impressions.
  last_accessed_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT videos_media_state_check
    CHECK (media_state IN ('QUEUED', 'DOWNLOADING', 'READY', 'EVICTED', 'FAILED'))
);

-- Full-text search over title and description, maintained by Postgres itself
-- so it can never drift from the row.
ALTER TABLE videos
  ADD COLUMN IF NOT EXISTS search_tsv tsvector
  GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('simple', coalesce(description, '')), 'B')
  ) STORED;

CREATE INDEX IF NOT EXISTS videos_search_idx     ON videos USING gin (search_tsv);
CREATE INDEX IF NOT EXISTS videos_categories_idx ON videos USING gin (categories);
CREATE INDEX IF NOT EXISTS videos_channel_idx    ON videos (channel_id);
CREATE INDEX IF NOT EXISTS videos_added_idx      ON videos (added_at DESC);
-- Supports the eviction sweep: oldest unpinned ready videos first.
CREATE INDEX IF NOT EXISTS videos_eviction_idx
  ON videos (last_accessed_at) WHERE media_state = 'READY' AND NOT pinned;

CREATE TABLE IF NOT EXISTS comments (
  id                text PRIMARY KEY,
  video_id          text        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
  parent_comment_id text REFERENCES comments (id) ON DELETE CASCADE,
  user_id           text        NOT NULL,
  author_handle     text        NOT NULL,
  author_avatar     text        NOT NULL DEFAULT '',
  body              text        NOT NULL,
  like_count        bigint      NOT NULL DEFAULT 0,
  pinned_by         text,
  published_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS comments_video_idx  ON comments (video_id, published_at DESC);
CREATE INDEX IF NOT EXISTS comments_parent_idx ON comments (parent_comment_id);

-- Per-user interaction. Kept in this schema on purpose: nearly every video read
-- needs it, and splitting it out would turn one query into an N+1 fan-out.
CREATE TABLE IF NOT EXISTS watch_progress (
  user_id          text        NOT NULL,
  video_id         text        NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
  position_seconds integer     NOT NULL DEFAULT 0,
  watched_fraction real        NOT NULL DEFAULT 0,
  last_watched_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, video_id)
);

CREATE INDEX IF NOT EXISTS watch_progress_recent_idx
  ON watch_progress (user_id, last_watched_at DESC);

CREATE TABLE IF NOT EXISTS reactions (
  user_id  text NOT NULL,
  video_id text NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
  reaction text NOT NULL CHECK (reaction IN ('LIKE', 'DISLIKE')),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, video_id)
);

CREATE TABLE IF NOT EXISTS subscriptions (
  user_id    text NOT NULL,
  channel_id text NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, channel_id)
);

CREATE TABLE IF NOT EXISTS watch_later (
  user_id    text NOT NULL,
  video_id   text NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, video_id)
);
