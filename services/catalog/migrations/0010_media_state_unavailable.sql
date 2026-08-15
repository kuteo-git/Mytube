-- A sixth media state: upstream will not hand the video over, ever.
--
-- FAILED already existed, and reusing it was tempting. It means "the attempt
-- did not work", and everything built on it offers to try again — which for a
-- members-only video is an offer that can only fail. One such video collected
-- thirteen jobs in two minutes, each a fresh extract against an upstream that
-- had already answered.
--
-- The CHECK constraint has to be widened before any service writes the new
-- value, or SetMediaState is rejected by the database and the worker reports a
-- constraint violation in place of the real reason.

SET search_path = catalog;

ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_media_state_check;
ALTER TABLE videos ADD CONSTRAINT videos_media_state_check
  CHECK (media_state IN ('QUEUED', 'DOWNLOADING', 'READY', 'EVICTED', 'FAILED', 'UNAVAILABLE'));
