-- Store the video's primary language code (e.g. "en", "vi", "ar", "th").
--
-- Filled from yt-dlp on a full metadata fetch or detected from the title when
-- only a flat playlist listing is available. Empty when neither source could
-- determine it — which means "unknown" rather than "none", and videos with an
-- unknown language are hidden from the feed when a language filter is active.

SET search_path = catalog;

ALTER TABLE videos ADD COLUMN IF NOT EXISTS language text NOT NULL DEFAULT '';
