-- Caption fetches upstream refused, so they can be asked for again later.
--
-- YouTube rate-limits the timedtext endpoint by address and answers 429 in
-- waves — measured across a dozen videos in one evening, the same video and the
-- same flags succeeding and failing minutes apart, with a plain yt-dlp run
-- outside this app refusing identically. It is not a property of the video and
-- not of the tool: a PO token provider was installed and measured, and the
-- caption tracks are listed either way (CLAUDE.md §4).
--
-- Until now the only attempts were the ones made while somebody was watching,
-- four of them over about ninety seconds. That asks the viewer's window to
-- coincide with upstream's, and when it does not the video has no captions for
-- as long as the page stays open — taking the translation and the read-aloud
-- with it, since both are built on the .vtt.
--
-- This is `RequeueFailed` for captions, and deliberately the same shape: one at
-- a time, growing waits, then stop. Not a job in `jobs`: that table is about
-- transferring a video and every reader of it — the activity page, the worker's
-- single slot, Retry — would have to learn about a kind of job that transfers
-- nothing.
--
-- In ingest's own schema, like unavailable_sources, for the same reason: "will
-- upstream hand this over" is ingest's question about upstream.

SET search_path = ingest;

CREATE TABLE IF NOT EXISTS subtitle_retries (
  source_url      text        PRIMARY KEY,
  video_id        text        NOT NULL DEFAULT '',
  -- The rendition the caption filenames are derived from, so a retry writes the
  -- names the first attempt would have (`mediaPaths`).
  height          integer     NOT NULL DEFAULT 0,
  attempts        integer     NOT NULL DEFAULT 0,
  -- yt-dlp's own line, kept for the same reason unavailable_sources keeps one.
  last_error      text        NOT NULL DEFAULT '',
  first_seen_at   timestamptz NOT NULL DEFAULT now(),
  last_attempt_at timestamptz NOT NULL DEFAULT now()
);

-- The sweep asks for the stalest row whose wait has elapsed, once a minute.
CREATE INDEX IF NOT EXISTS subtitle_retries_due_idx
  ON subtitle_retries (last_attempt_at);
