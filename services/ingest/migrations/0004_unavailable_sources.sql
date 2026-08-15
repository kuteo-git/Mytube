-- What upstream has permanently refused.
--
-- yt-dlp failing is ordinary and usually temporary — a 429, a network that
-- dropped, a yt-dlp that has fallen behind YouTube (CLAUDE.md §8). Those are
-- worth retrying. A members-only, private or deleted video is not: asking again
-- gets the same answer, and every ask is a fresh extract counted against this
-- IP. One members-only video collected thirteen jobs in two minutes before this
-- table existed, because nothing anywhere remembered the refusal.
--
-- It lives in ingest's own schema on purpose. "Can this URL still be fetched"
-- is ingest's question about upstream, not the catalogue's about the library;
-- catalog is told separately, through the service, so it can show the viewer
-- why (CLAUDE.md §3, rule 1).

SET search_path = ingest;

CREATE TABLE IF NOT EXISTS unavailable_sources (
  source_url  text        PRIMARY KEY,
  video_id    text        NOT NULL DEFAULT '',
  -- members_only | private | removed | unavailable
  reason      text        NOT NULL,
  -- yt-dlp's own line. A judgement this final has to show its evidence.
  detail      text        NOT NULL DEFAULT '',
  first_seen_at timestamptz NOT NULL DEFAULT now(),
  -- Pushed to catalog through the service, which may be down when the refusal
  -- is discovered. Null means "catalog has not been told yet".
  reported_at timestamptz
);

-- The seven videos already in this state, read from the failures they left
-- behind. Without this every one of them has to fail at least once more before
-- anything notices, which is the loop this table exists to end.
--
-- The patterns are the permanent ones only, and each is anchored on yt-dlp's
-- own wording. Anything mentioning a rate limit or a retry is excluded outright:
-- a temporary refusal recorded here would take a video out of the library until
-- somebody pressed Retry by hand.
INSERT INTO unavailable_sources (source_url, video_id, reason, detail)
SELECT DISTINCT ON (j.source_url)
       j.source_url,
       coalesce(j.video_id, ''),
       CASE
         WHEN j.error_message ILIKE '%members-only content%' THEN 'members_only'
         WHEN j.error_message ILIKE '%private video%'
           OR j.error_message ILIKE '%this video is private%' THEN 'private'
         WHEN j.error_message ILIKE '%has been removed%'
           OR j.error_message ILIKE '%has been terminated%' THEN 'removed'
         ELSE 'unavailable'
       END,
       j.error_message
  FROM jobs j
 WHERE j.state = 'FAILED'
   AND (
         j.error_message ILIKE '%members-only content%'
      OR j.error_message ILIKE '%private video%'
      OR j.error_message ILIKE '%this video is private%'
      OR j.error_message ILIKE '%has been removed%'
      OR j.error_message ILIKE '%has been terminated%'
   )
   AND j.error_message NOT ILIKE '%429%'
   AND j.error_message NOT ILIKE '%too many requests%'
   AND j.error_message NOT ILIKE '%try again later%'
 ORDER BY j.source_url, j.created_at DESC
    ON CONFLICT (source_url) DO NOTHING;
