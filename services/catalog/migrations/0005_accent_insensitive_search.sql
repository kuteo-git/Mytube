-- Accent-insensitive search.
--
-- Vietnamese is written with diacritics, but they are routinely dropped when
-- typing and yt-dlp itself reports some channel names without them: the
-- library stores "Tinh te" while the person searching types "tinh tế". Without
-- folding, those never meet, and roughly half the library becomes unfindable.
--
-- unaccent() is marked STABLE rather than IMMUTABLE because its dictionary can
-- be reloaded, so it cannot be used in a generated column directly. Pinning the
-- dictionary by name makes the call deterministic, which is what the wrapper
-- below asserts.
--
-- Requires superuser for CREATE EXTENSION; run as the database owner.

CREATE EXTENSION IF NOT EXISTS unaccent;

SET search_path = catalog, public;

CREATE OR REPLACE FUNCTION catalog.immutable_unaccent(text)
RETURNS text
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
STRICT
AS $$ SELECT public.unaccent('public.unaccent'::regdictionary, $1) $$;

ALTER TABLE catalog.videos DROP COLUMN IF EXISTS search_tsv;

ALTER TABLE catalog.videos
  ADD COLUMN search_tsv tsvector
  GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', catalog.immutable_unaccent(coalesce(title, ''))), 'A') ||
    setweight(to_tsvector('simple', catalog.immutable_unaccent(coalesce(description, ''))), 'B')
  ) STORED;

CREATE INDEX IF NOT EXISTS videos_search_idx ON catalog.videos USING gin (search_tsv);

-- Channel names are matched with ILIKE, so they need their own folded index.
CREATE INDEX IF NOT EXISTS channels_name_unaccent_idx
  ON catalog.channels (catalog.immutable_unaccent(lower(name)));
