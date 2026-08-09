-- How many times each video has been put in front of somebody.
--
-- The table already recorded *that* a video had been shown, which is what the
-- 24-hour repeat penalty needs. It could not answer "this has been offered eight
-- times and nobody has ever opened it", because every impression overwrote the
-- last one. That question is the whole of exploration: the discovery share was
-- picking by score over a set that barely changes, so the same unfamiliar videos
-- were offered again and again while most of the library was never offered once.
--
-- Existing rows default to 1: they were shown at least once, and treating them as
-- never-shown would hand the entire discovery share to material already rejected.

SET search_path = recsys;

ALTER TABLE impressions
  ADD COLUMN IF NOT EXISTS shown_count integer NOT NULL DEFAULT 1;
