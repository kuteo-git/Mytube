-- Development seed for the recommendation service.
--
-- Deliberately a separate file with a separate role: recsys keeps its own copy
-- of the behaviour signals and must never read catalog's tables. In production
-- these rows arrive through RecordSignal as the user interacts.
--
--   PGPASSWORD=recsys_dev psql -h localhost -U recsys_svc -d localyoutube -f db/seed_dev_recsys.sql

SET search_path = recsys;

TRUNCATE signals, impressions;

INSERT INTO signals (user_id, type, video_id, watched_fraction, occurred_at) VALUES
  ('u_luc', 'WATCH', 'v1',  0.34, now() - interval '3 hours'),
  ('u_luc', 'WATCH', 'v10', 0.72, now() - interval '2 days'),
  ('u_luc', 'WATCH', 'v6',  1.00, now() - interval '9 days'),
  ('u_luc', 'LIKE',  'v1',  0,    now() - interval '3 hours'),
  ('u_luc', 'LIKE',  'v3',  0,    now() - interval '5 days');

-- SUBSCRIBE signals carry the channel id in video_id.
INSERT INTO signals (user_id, type, video_id, occurred_at) VALUES
  ('u_luc', 'SUBSCRIBE', 'c1', now() - interval '200 days'),
  ('u_luc', 'SUBSCRIBE', 'c4', now() - interval '100 days'),
  ('u_luc', 'SUBSCRIBE', 'c5', now() - interval '50 days');
