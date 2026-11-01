-- Cast schema. Owned by cast_svc.
--
-- A session is kept here rather than in memory so that it survives a restart
-- and every client in the house sees the same one. The queue is a snapshot of
-- what each item was when it was queued, so a remote can draw it without
-- asking the catalogue.

SET search_path = "cast";

CREATE TABLE IF NOT EXISTS sessions (
  id               uuid        PRIMARY KEY,
  device_id        text        NOT NULL,
  device_kind      text        NOT NULL CHECK (device_kind IN ('r1', 'google')),
  device_name      text        NOT NULL DEFAULT '',
  started_by       text        NOT NULL,
  queue            jsonb       NOT NULL,
  current_index    int         NOT NULL,
  position_s       int         NOT NULL DEFAULT 0,
  duration_s       int         NOT NULL DEFAULT 0,
  volume           int         NOT NULL DEFAULT 0,
  state            text        NOT NULL CHECK (state IN ('loading', 'playing', 'paused', 'ended')),
  ended_reason     text,
  -- What was handed to the speaker, and the one before it, which is how a
  -- takeover is told from our own previous song still sounding.
  loaded_media_key text        NOT NULL DEFAULT '',
  prev_media_key   text        NOT NULL DEFAULT '',
  load_acked       boolean     NOT NULL DEFAULT false,
  loading_since    timestamptz,
  last_seen_ok     timestamptz,
  last_progress_at timestamptz,
  version          int         NOT NULL DEFAULT 1,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);

-- One session playing per speaker. A new one ends the old in the same
-- transaction; this is what makes two at once impossible rather than unlikely.
CREATE UNIQUE INDEX IF NOT EXISTS sessions_one_active_per_device
  ON sessions (device_id) WHERE state <> 'ended';
