-- Categories become topics.
--
-- The column used to hold YouTube's own category strings, which turned out to
-- be useless: YouTube has roughly fifteen of them globally, so real videos
-- landed in buckets like "Entertainment" that say nothing. Topics are the names
-- from topics.yaml, assigned by which source a video was discovered in.

SET search_path = catalog;

ALTER TABLE videos RENAME COLUMN categories TO topics;

ALTER INDEX IF EXISTS videos_categories_idx RENAME TO videos_topics_idx;

-- Existing rows carry YouTube categories, which do not correspond to any topic.
-- Clearing them lets the scanner assign real topics on its next pass.
UPDATE videos SET topics = '{}';
