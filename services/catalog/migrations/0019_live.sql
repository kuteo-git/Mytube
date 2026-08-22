-- Whether a video is a broadcast still on air.
--
-- Unlike is_short, this answer expires. A Short is a Short for ever; a live
-- stream ends, and the row that said "on air" goes on saying it until something
-- asks again. So the answer is stored beside the moment it was given, and a
-- reader that cares about right now must look at both.
--
-- It is yt-dlp's own word rather than a boolean, because "was_live" is worth
-- keeping too:
--   NULL          — never asked
--   'is_live'     — on air when last asked
--   'is_upcoming' — scheduled, not started; deliberately NOT offered as Live,
--                   since pressing it plays nothing
--   'was_live'    — a finished broadcast, which is an ordinary video with an
--                   ordinary recording
--   'not_live'    — asked, and it was never a broadcast at all
--
-- A boolean would collapse the last three into one, and the player needs the
-- difference: it is what tells a broadcast from its recording without going
-- back to YouTube to ask.
--
-- Where the answer comes from: a flat listing of the channel's /streams tab.
-- Measured on ABC News, one request each — /videos carries live_status None on
-- 40 of 40 and lists no broadcast at all, while /streams carries 1 is_live and
-- 39 was_live. The scanner walks /videos, which is the whole reason nothing in
-- this library has ever known a broadcast was happening. A flat listing is the
-- cheap kind of request, so this costs nothing that §8 risk 6 counts.
ALTER TABLE catalog.videos
  ADD COLUMN IF NOT EXISTS live_status text,
  ADD COLUMN IF NOT EXISTS live_checked_at timestamptz;

-- What the Live chip reads: rows on air, freshest answer first.
--
-- Partial, because this is a handful of rows against 32,000 — everything else
-- is either not a broadcast or a finished one, and neither can ever match.
-- live_checked_at leads because the query's real question is "still true?", and
-- the staleness cut (30 minutes, three passes) is applied to that column.
CREATE INDEX IF NOT EXISTS videos_live_now_idx
  ON catalog.videos (live_checked_at DESC)
  WHERE live_status = 'is_live';
