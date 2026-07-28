-- How far into each source the library has been filled. Deepening resumes from
-- here rather than re-reading the newest forty uploads on every pass.

SET search_path = ingest;

CREATE TABLE IF NOT EXISTS source_cursors (
  source_url  text        PRIMARY KEY,
  next_offset integer     NOT NULL DEFAULT 0,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
