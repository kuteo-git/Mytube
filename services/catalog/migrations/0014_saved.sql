-- Save becomes a personal shelf, and pinning stays a fact about the disk.
--
-- The two were one boolean on the video, so one member pressing Save put the
-- video on every member's Saved page — the only piece of per-viewer state in
-- this schema that was not keyed by user_id, next to watch_progress, reactions,
-- subscriptions and watch_later, which all are.
--
-- They cannot be separated by adding user_id to videos.pinned, because they are
-- genuinely two different facts. Whose shelf a video is on is personal; whether
-- the eviction sweep may delete its bytes is a question about one disk and one
-- 300 GiB budget, and it must be answered "no" if *anybody* has saved it.
--
-- So the shelf is a table, and videos.pinned becomes derived: true when at least
-- one row here points at the video. The eviction sweep and its partial index in
-- 0001_init.sql go on reading the column and are untouched.
CREATE TABLE IF NOT EXISTS saved (
  user_id    text NOT NULL,
  video_id   text NOT NULL REFERENCES videos (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, video_id)
);

-- The Saved page reads newest-first for one member.
CREATE INDEX IF NOT EXISTS saved_user_created_idx ON saved (user_id, created_at DESC);

-- Everything saved before there were profiles belongs to DEV_USER_ID, which is
-- where every other pre-picker record already went: the gateway falls back to it
-- for a request with no X-User-Id, and recsys.signals holds the whole household's
-- history under it. Answering this question differently here would be two
-- answers to one question.
--
-- Left as a literal rather than read from the environment because a migration
-- runs once and must produce the same database twice. 'u_luc' is the default in
-- services/gateway/cmd/gateway/main.go; an installation that changed DEV_USER_ID
-- should change it here before applying.
INSERT INTO saved (user_id, video_id)
SELECT 'u_luc', id FROM videos WHERE pinned
ON CONFLICT DO NOTHING;
