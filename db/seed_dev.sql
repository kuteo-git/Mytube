-- Development seed. Mirrors the mock data the web client was built against so
-- the UI looks the same once it is pointed at the real service.
--
--   PGPASSWORD=catalog_dev psql -h localhost -U catalog_svc -d localyoutube -f db/seed_dev.sql

SET search_path = catalog;

TRUNCATE comments, watch_progress, reactions, subscriptions, watch_later, videos, channels CASCADE;

INSERT INTO channels (id, name, handle, subscriber_count, verified) VALUES
  ('c1', 'SOOBIN Official',   '@soobinofficial',  1590000, true),
  ('c2', 'Cường Lê Hà',       '@cuonglehà',        412000, false),
  ('c3', 'Aurora Work',       '@aurorawork',        88000, false),
  ('c4', 'Phạm Vlog',         '@phamvlog',        2300000, true),
  ('c5', 'NetworkChuck',      '@networkchuck',    4100000, true),
  ('c6', 'Video Game Worlds', '@videogameworlds',  620000, false),
  ('c7', 'Tài Xài Tech',      '@taixaitech',       155000, false),
  ('c8', 'Mood Capture',      '@moodcapture',       74000, false);

INSERT INTO videos (
  id, title, channel_id, duration_seconds, view_count, published_at, added_at,
  description, hashtags, categories, media_state, media_path, size_bytes, source_url
) VALUES
  ('v1',  'SOOBIN - HIGH HEELS | OFFICIAL MUSIC VIDEO', 'c1',  254, 2397183,
   now() - interval '1200 days', now() - interval '2 days',
   E'KOSMIK Album will be out on 15.11.22\n\nAudio is now available on:\nhttps://wmvn.lnk.to/KOSMIK\n\nFOLLOW ONLINE:\nFacebook / Instagram / Spotify / Apple Music\n\n© Copyright by SpaceSpeakers Label',
   '{#SOOBIN,#CaoGot,#SpaceSpeakers}', '{Music}', 'READY', 'v1/1080p.mp4', 93323264, 'https://www.youtube.com/watch?v=v1'),

  ('v2',  'ĐỪNG MUA Nintendo Switch 2… nếu bạn thuộc 1 trong số này!', 'c2', 1899, 20400,
   now() - interval '180 days', now() - interval '1 day',
   'Review chi tiết Nintendo Switch 2.', '{}', '{Gaming,"Computer Hardware"}', 'READY', 'v2/1080p.mp4', 698351616, 'https://www.youtube.com/watch?v=v2'),

  ('v3',  'Deep work music | Minimalist ambient beats for deep focus & flow state concentration music', 'c3', 7381, 254000,
   now() - interval '21 days', now() - interval '3 days',
   'Ambient beats for deep focus.', '{}', '{Music,"Meditation music"}', 'READY', 'v3/1080p.mp4', 2714566656, 'https://www.youtube.com/watch?v=v3'),

  ('v4',  'ĐEN VÂU || Tổng Hợp Những Ca Khúc Hay Nhất Của Đen Vâu', 'c4', 3704, 2300000,
   now() - interval '730 days', now() - interval '12 days',
   'Tổng hợp nhạc Đen Vâu.', '{}', '{Music,Playlists}', 'READY', 'v4/1080p.mp4', 1362100224, 'https://www.youtube.com/watch?v=v4'),

  ('v5',  'Hóa ra SONY đã trải qua cay đắng như vậy', 'c2', 785, 164000,
   now() - interval '7 days', now() - interval '4 days',
   'Câu chuyện về Sony.', '{}', '{News}', 'READY', 'v5/1080p.mp4', 288358400, 'https://www.youtube.com/watch?v=v5'),

  ('v6',  'Death Stranding | Low Roar and Cinematic Ambience', 'c6', 3521, 2900000,
   now() - interval '1095 days', now() - interval '20 days',
   'Cinematic ambience.', '{}', '{Gaming,"Sound design"}', 'READY', 'v6/1080p.mp4', 1294991360, 'https://www.youtube.com/watch?v=v6'),

  ('v7',  'you need to use Hermes RIGHT NOW!! (goodbye SSH)', 'c5', 1959, 1300000,
   now() - interval '60 days', now() - interval '5 days',
   'Networking tutorial.', '{}', '{Gadgets}', 'READY', 'v7/1080p.mp4', 720371712, 'https://www.youtube.com/watch?v=v7'),

  ('v8',  'Lần đầu xài thử kính AR, chơi game quá đã! | XREAL One Pro', 'c7', 873, 4600,
   now() - interval '4 days', now() - interval '4 days',
   'Trải nghiệm kính AR.', '{}', '{Gadgets,"Computer Hardware"}', 'READY', 'v8/1080p.mp4', 320864256, 'https://www.youtube.com/watch?v=v8'),

  -- Evicted: bytes reclaimed by the LRU sweep, metadata deliberately kept.
  ('v9',  'Best of Imagine Dragons 2026 — Top Songs Playlist', 'c8', 4414, 180000,
   now() - interval '90 days', now() - interval '30 days',
   'Playlist tổng hợp.', '{}', '{Music,Playlists}', 'EVICTED', '', 0, 'https://www.youtube.com/watch?v=v9'),

  ('v10', 'SOOBIN X SLIMV - THE PLAYAH (Special Performance)', 'c1', 507, 71000000,
   now() - interval '1825 days', now() - interval '40 days',
   'Special performance.', '{#SOOBIN}', '{Music}', 'READY', 'v10/1080p.mp4', 186122240, 'https://www.youtube.com/watch?v=v10'),

  ('v11', 'Vĩ Đại Do Lựa Chọn: Vì Sao May Mắn Không Phải Là Tất Cả', 'c4', 1115, 12400,
   now() - interval '1 day', now() - interval '1 day',
   'Tóm tắt sách.', '{}', '{News}', 'READY', 'v11/1080p.mp4', 409993216, 'https://www.youtube.com/watch?v=v11'),

  ('v12', 'IMAGINE DRAGONS GREATEST HITS 2026 — Full Album', 'c8', 2610, 124000,
   now() - interval '30 days', now() - interval '25 days',
   'Full album.', '{}', '{Music,Albums}', 'READY', 'v12/1080p.mp4', 959447040, 'https://www.youtube.com/watch?v=v12'),

  ('v13', 'Full ĐÊM NHẠC - Bạch Công Khanh show acoustic', 'c4', 7767, 38,
   now() - interval '2 hours', now() - interval '2 hours',
   'Live acoustic.', '{}', '{Music,Orchestra}', 'READY', 'v13/1080p.mp4', 2856517632, 'https://www.youtube.com/watch?v=v13'),

  ('v14', 'In The End - Dark Ambient Music for Deep Focus & Study', 'c3', 7209, 53000,
   now() - interval '60 days', now() - interval '45 days',
   'Dark ambient.', '{}', '{Music,"Meditation music","Sound design"}', 'READY', 'v14/1080p.mp4', 2650800128, 'https://www.youtube.com/watch?v=v14'),

  -- Still being fetched by the ingest worker.
  ('v15', 'Mưa Buồn || Đàn Bầu Cover - Nhạc Không Lời', 'c8', 2400, 890000,
   now() - interval '400 days', now() - interval '60 days',
   'Nhạc không lời.', '{}', '{Music,Orchestra}', 'DOWNLOADING', '', 0, 'https://www.youtube.com/watch?v=v15');

-- Two seeded accounts, matching the Phase 1 decision to skip a signup screen.
INSERT INTO subscriptions (user_id, channel_id) VALUES
  ('u_luc', 'c1'), ('u_luc', 'c4'), ('u_luc', 'c5');

INSERT INTO watch_progress (user_id, video_id, position_seconds, watched_fraction, last_watched_at) VALUES
  ('u_luc', 'v1',  86,  0.34, now() - interval '3 hours'),
  ('u_luc', 'v10', 365, 0.72, now() - interval '2 days'),
  ('u_luc', 'v6',  3521, 1.00, now() - interval '9 days');

INSERT INTO reactions (user_id, video_id, reaction) VALUES
  ('u_luc', 'v1', 'LIKE'), ('u_luc', 'v3', 'LIKE');

INSERT INTO comments (id, video_id, parent_comment_id, user_id, author_handle, body, like_count, pinned_by, published_at) VALUES
  ('k1', 'v1', NULL, 'u_soobin', '@SoobinOfficial',
   'Một bài hát tiếp theo trong album KOSMIK dành tặng cho những người phụ nữ tuyệt vời trên thế giới này 💙',
   577, '@SoobinOfficial', now() - interval '1100 days'),
  ('k1r1', 'v1', 'k1', 'u_fan', '@fanclub.vn', 'Anh ơi bao giờ ra MV tiếp theo ạ', 12, NULL, now() - interval '1050 days'),
  ('k1r2', 'v1', 'k1', 'u_minh', '@minh.tran', 'Album hay quá anh ơi 🔥', 8, NULL, now() - interval '1000 days'),
  ('k2', 'v1', NULL, 'u_ngocmai', '@ngocmaingo1008',
   'Bài này đỉnh chóp như vậy, vibe hiện đại như vậy sao ít view thế nhỉ 😔 Thỉnh thoảng mình lại vào cày view cho nó 😂',
   37, NULL, now() - interval '330 days'),
  ('k2r1', 'v1', 'k2', 'u_hoang', '@hoang.le', 'Same, nghe hoài không chán', 3, NULL, now() - interval '320 days'),
  ('k3', 'v1', NULL, 'u_sibuniu', '@sibuniu.tksvan',
   'bài này thật sự xứng đáng được viral hơn nữa vì quá là hay, cảm giác 10 năm nữa nghe lại cũng không lỗi thời luôn',
   4, NULL, now() - interval '60 days'),
  ('k4', 'v1', NULL, 'u_ngaly', '@NgaLy-z4n', E'Cao gót\nLả lướt tới những nơi em đi phải siêu lòng', 3, NULL, now() - interval '30 days'),
  ('k5', 'v1', NULL, 'u_llq', '@LLQ-x7s', 'Cao gót ở concert ALLROUNDER là thần', 9, NULL, now() - interval '210 days');
