-- Subtitle tracks fetched alongside the media file.
--
-- A separate table rather than an array column: a track has a language, a
-- label, a path and a flag for whether it was machine generated, and asking
-- "which languages does this video have" should not mean parsing an array.

SET search_path = catalog;

CREATE TABLE IF NOT EXISTS subtitles (
  video_id  text NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
  -- BCP-47 as reported by the source, e.g. "en", "vi", "en-US".
  language  text NOT NULL,
  label     text NOT NULL DEFAULT '',
  -- Relative to the media root, alongside the video file.
  path      text NOT NULL,
  -- Auto-generated captions are noticeably worse; the UI marks them.
  generated boolean NOT NULL DEFAULT false,
  PRIMARY KEY (video_id, language)
);
