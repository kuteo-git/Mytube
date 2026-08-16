-- YouTube's own recommendations, imported as material and never as ranking.
--
-- §6's claim is that every score in this library can be explained. YouTube's
-- ordering cannot be — not by anyone here — so what its home feed offers comes
-- in as videos and is fenced by recsys into the discovery bucket, exactly as
-- ExpandLibrary's related finds already are. It is a fourth answer to "who
-- asked for this", not a fourth kind of ranking.
ALTER TABLE catalog.videos DROP CONSTRAINT IF EXISTS videos_discovered_via_check;
ALTER TABLE catalog.videos
  ADD CONSTRAINT videos_discovered_via_check
  CHECK (discovered_via IS NULL OR discovered_via IN ('SOURCE','RELATED','SEARCH','YOUTUBE_REC'));
