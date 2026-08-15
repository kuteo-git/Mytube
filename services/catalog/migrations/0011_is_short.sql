-- Whether a video is a YouTube Short.
--
-- Not derivable from anything already stored, and in particular not from
-- duration. Measured against YouTube: a 14-second video and a 9-second one both
-- answered /shorts/<id> with a 303 back to /watch — they are ordinary short
-- clips — while 40-second and 59-second videos answered 200. Length is what a
-- Short usually has, not what it is.
--
-- Three states, so a tri-state rather than a boolean:
--   NULL  — never asked
--   true  — YouTube served /shorts/<id>
--   false — YouTube redirected to /watch
--
-- NULL matters because the answer costs an HTTP request and this table already
-- holds thousands of rows. Without it there is no way to tell a video that was
-- checked and cleared from one nobody has got to yet, and the pass would either
-- re-ask about everything forever or never finish the backlog.
ALTER TABLE catalog.videos
  ADD COLUMN IF NOT EXISTS is_short boolean;

-- The pass's working set: rows still unanswered, likeliest first.
--
-- The leading column is the same CASE the query orders by. A partial index on
-- published_at alone still made the planner sort every unanswered row, which is
-- thousands of them on every pass.
CREATE INDEX IF NOT EXISTS videos_is_short_unknown_idx
  ON catalog.videos (
    (CASE
       WHEN duration_seconds BETWEEN 1 AND 180 THEN 0
       WHEN duration_seconds IS NULL OR duration_seconds = 0 THEN 1
       ELSE 2
     END),
    published_at DESC NULLS LAST
  )
  WHERE is_short IS NULL;
