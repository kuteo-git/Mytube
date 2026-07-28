-- Merge channels that exist twice under two id forms.
--
-- A flat playlist listing often reports a video's owner as a handle
-- ("@mkbhd") while channel metadata reports the channel id ("UC..."). Both were
-- written straight through as channel ids, so one channel became two rows —
-- sixty of them — and the sidebar and channel pages showed each twice.
--
-- The channel id form wins: it is stable, whereas a handle can be changed by
-- its owner. The scanner now resolves to it before writing (see applyOwner in
-- services/ingest/internal/usecase/scanner.go), so this only has to clean up
-- what the earlier behaviour left behind.

SET search_path = catalog;

BEGIN;

CREATE TEMP TABLE channel_merge ON COMMIT DROP AS
SELECT h.id AS handle_id, u.id AS uc_id
FROM channels h
JOIN channels u
  ON lower(h.handle) = lower(u.handle)
 AND h.handle <> ''
 AND h.id LIKE '@%'
 AND u.id LIKE 'UC%';

-- Keep whatever the duplicate knew that the survivor does not. Artwork in
-- particular tends to land on only one of the two rows.
UPDATE channels u
SET avatar_path      = COALESCE(NULLIF(u.avatar_path, ''), h.avatar_path),
    banner_path      = COALESCE(NULLIF(u.banner_path, ''), h.banner_path),
    subscriber_count = GREATEST(u.subscriber_count, h.subscriber_count),
    verified         = u.verified OR h.verified
FROM channel_merge m
JOIN channels h ON h.id = m.handle_id
WHERE u.id = m.uc_id;

-- A user subscribed under both forms would collide on the primary key, so drop
-- the duplicate rather than move it.
DELETE FROM subscriptions s
USING channel_merge m
WHERE s.channel_id = m.handle_id
  AND EXISTS (
    SELECT 1 FROM subscriptions t
    WHERE t.user_id = s.user_id AND t.channel_id = m.uc_id
  );

UPDATE subscriptions s SET channel_id = m.uc_id
FROM channel_merge m WHERE s.channel_id = m.handle_id;

UPDATE videos v SET channel_id = m.uc_id
FROM channel_merge m WHERE v.channel_id = m.handle_id;

DELETE FROM channels c USING channel_merge m WHERE c.id = m.handle_id;

COMMIT;
