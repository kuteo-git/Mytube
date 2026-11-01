-- Narration on the speaker. Owned by cast_svc.
--
-- `narration` is what the remote asked for; `loaded_narrated` is whether the
-- speaker was handed the narrated sound for the current item. They differ for
-- as long as the gateway takes to make it, and the song plays as it is
-- meanwhile.

SET search_path = "cast";

ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS narration       boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS loaded_narrated boolean NOT NULL DEFAULT false;
