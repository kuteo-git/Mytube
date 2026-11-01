-- A song that speaks Vietnamese is not narrated (its narration would read its
-- Vietnamese captions over its own voice) unless narration was switched on
-- while it played. This is that song, so the next one is decided by its own
-- language again. '' for none.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS narration_forced_for text NOT NULL DEFAULT '';
