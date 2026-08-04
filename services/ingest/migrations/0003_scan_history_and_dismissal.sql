-- Two additions to the Activity page's sources of truth.
--
-- scans: the scanner kept its last result in a variable, so the only question
-- the page could answer was "how did the most recent pass go" — and a restart
-- could not even answer that. The question people actually ask spans days:
-- "this channel has no new videos, has the scan been running at all?"
--
-- jobs.dismissed_at: a finished or failed job had no action on it but reading.
-- Dismissing is what turns the download list into something that can be kept
-- tidy. The column rather than a delete, because the player reads this same
-- table to learn that a copy has landed, and removing rows from under it is a
-- fault this project has already had once.

SET search_path = ingest;

CREATE TABLE IF NOT EXISTS scans (
  id              bigserial   PRIMARY KEY,
  started_at      timestamptz NOT NULL,
  duration_ms     bigint      NOT NULL DEFAULT 0,
  sources_scanned integer     NOT NULL DEFAULT 0,
  sources_failed  integer     NOT NULL DEFAULT 0,
  videos_seen     integer     NOT NULL DEFAULT 0,
  videos_added    integer     NOT NULL DEFAULT 0,
  -- Verbatim, one per source that could not be read. NOT NULL because pgx
  -- encodes a nil slice as NULL, which a NOT NULL array column rejects — a
  -- trap already documented in CLAUDE.md.
  errors          text[]      NOT NULL DEFAULT '{}'
);

-- The page reads these newest first and nothing else reads them at all.
CREATE INDEX IF NOT EXISTS scans_started_at_idx ON scans (started_at DESC);

ALTER TABLE jobs ADD COLUMN IF NOT EXISTS dismissed_at timestamptz;
