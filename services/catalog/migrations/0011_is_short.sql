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

-- The backfill's working set: rows still unanswered, newest first.
CREATE INDEX IF NOT EXISTS videos_is_short_unknown_idx
  ON catalog.videos (published_at DESC NULLS LAST)
  WHERE is_short IS NULL;
