-- QUEUED never meant queued. It is the column's DEFAULT, so every row the
-- scanner has ever inserted carries it — 22 of the 24 videos on the first page
-- of the feed, against zero jobs queued or running anywhere in ingest. What it
-- actually means is "no file on this disk", which is the same thing a brand new
-- row and a video nobody will ever download have in common.
--
-- The name mattered because something reads it. A card showing a video waiting
-- in a download queue that does not exist is the dead button of §5 wearing a
-- label instead of a border, and there were eight thousand of them.
--
-- Whether a transfer is queued is a question about `ingest.jobs`, which has its
-- own QUEUED and means it: a row there is a job a worker will claim. That one
-- is not touched.
--
-- ABSENT joins EVICTED in describing the disk rather than an intention. The
-- difference between them is kept and is worth keeping: EVICTED means there was
-- a file and the sweep took it back, which is why the UI can offer "Removed —
-- press to fetch again"; ABSENT means there has never been one.

SET search_path = catalog;

-- Dropped first: the constraint names the old value, so it would refuse the
-- rows on their way to the new one.
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_media_state_check;

ALTER TABLE videos ALTER COLUMN media_state SET DEFAULT 'ABSENT';

UPDATE videos SET media_state = 'ABSENT' WHERE media_state = 'QUEUED';

ALTER TABLE videos ADD CONSTRAINT videos_media_state_check
  CHECK (media_state IN ('ABSENT', 'DOWNLOADING', 'READY', 'EVICTED', 'FAILED', 'UNAVAILABLE'));
