-- Where a video came from.
--
-- There was no way to tell, and the absence cost a week of guessing. The
-- library reached 708 channels against 87 subscribed and 6 curated sources,
-- and when the question "which of these did anybody ask for" was finally put to
-- the data, nothing in it could answer.
--
-- The obvious proxy was tried and is wrong: expansion stored videos unfiled, so
-- "no topic" looked like "arrived uninvited" — until the metadata backfill fills
-- YouTube's own category in for everything, erasing the distinction. 4543
-- topicless videos here belong to subscribed channels.
--
--   SOURCE   — a topics.yaml source or a subscribed channel's uploads
--   RELATED  — InnerTube related, reached by ExpandLibrary
--   SEARCH   — the viewer typed a query, or (historically) expansion did
--   NULL     — arrived before this column existed, and unknowable
--
-- NULL is not backfillable and is deliberately not treated as suspicious:
-- guessing retroactively is what the proxy above already got wrong.
ALTER TABLE catalog.videos
  ADD COLUMN IF NOT EXISTS discovered_via text;

ALTER TABLE catalog.videos
  DROP CONSTRAINT IF EXISTS videos_discovered_via_check;
ALTER TABLE catalog.videos
  ADD CONSTRAINT videos_discovered_via_check
  CHECK (discovered_via IS NULL OR discovered_via IN ('SOURCE','RELATED','SEARCH'));

-- Ranking asks for "everything not reached by expansion", so the index is on
-- the rows that are.
CREATE INDEX IF NOT EXISTS videos_discovered_via_idx
  ON catalog.videos (discovered_via)
  WHERE discovered_via IS NOT NULL;
