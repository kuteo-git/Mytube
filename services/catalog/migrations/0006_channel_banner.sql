-- Channel artwork, served by Caddy under /media like every other asset. Paths
-- are relative so the client works under any LAN hostname or scheme.

SET search_path = catalog;

ALTER TABLE channels ADD COLUMN IF NOT EXISTS banner_path text NOT NULL DEFAULT '';
