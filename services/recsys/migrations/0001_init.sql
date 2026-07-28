-- Recommendation schema. Owned by recsys_svc.
--
-- Signals are stored here rather than read from catalog: the two services must
-- not share tables. Catalog owns "what the user watched" as product state;
-- recsys keeps its own append-only copy as ranking input.

SET search_path = recsys;

CREATE TABLE IF NOT EXISTS signals (
  id               bigserial PRIMARY KEY,
  user_id          text        NOT NULL,
  type             text        NOT NULL,
  video_id         text        NOT NULL DEFAULT '',
  query            text        NOT NULL DEFAULT '',
  watched_fraction real        NOT NULL DEFAULT 0,
  occurred_at      timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT signals_type_check CHECK (
    type IN ('WATCH', 'LIKE', 'DISLIKE', 'SUBSCRIBE', 'UNSUBSCRIBE', 'SEARCH', 'SKIP')
  )
);

CREATE INDEX IF NOT EXISTS signals_user_time_idx  ON signals (user_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS signals_user_video_idx ON signals (user_id, video_id);

-- What the user has already been shown, so the grid stops repeating itself.
CREATE TABLE IF NOT EXISTS impressions (
  user_id  text        NOT NULL,
  video_id text        NOT NULL,
  shown_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, video_id)
);

CREATE INDEX IF NOT EXISTS impressions_recent_idx ON impressions (user_id, shown_at DESC);
