-- Ingest schema. Owned by ingest_svc.
--
-- The job table is also the queue. A dedicated broker would add a process to
-- operate for no benefit at this scale, while SKIP LOCKED gives exactly what a
-- download pipeline needs: durable state, visible progress, and safe retries
-- across restarts.

SET search_path = ingest;

CREATE TABLE IF NOT EXISTS jobs (
  id               text PRIMARY KEY,
  source_url       text        NOT NULL,
  -- Filled in once metadata resolution succeeds.
  video_id         text        NOT NULL DEFAULT '',
  title            text        NOT NULL DEFAULT '',
  state            text        NOT NULL DEFAULT 'QUEUED',
  preferred_height integer     NOT NULL DEFAULT 1080,
  progress         real        NOT NULL DEFAULT 0,
  downloaded_bytes bigint      NOT NULL DEFAULT 0,
  total_bytes      bigint      NOT NULL DEFAULT 0,
  error_message    text        NOT NULL DEFAULT '',
  attempts         integer     NOT NULL DEFAULT 0,
  requested_by     text        NOT NULL DEFAULT '',
  -- Held by the worker that claimed the job; lets a crashed worker's job be
  -- reclaimed once the lease goes stale instead of being stuck forever.
  lease_expires_at timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  started_at       timestamptz,
  finished_at      timestamptz,

  CONSTRAINT jobs_state_check CHECK (
    state IN ('QUEUED', 'RUNNING', 'SUCCEEDED', 'FAILED', 'CANCELLED')
  )
);

-- One active job per URL: re-submitting something already downloading should
-- attach to the existing job rather than start a second download.
CREATE UNIQUE INDEX IF NOT EXISTS jobs_active_source_idx
  ON jobs (source_url) WHERE state IN ('QUEUED', 'RUNNING');

CREATE INDEX IF NOT EXISTS jobs_claim_idx
  ON jobs (created_at) WHERE state = 'QUEUED';

CREATE INDEX IF NOT EXISTS jobs_recent_idx ON jobs (created_at DESC);
