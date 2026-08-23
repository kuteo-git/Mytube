# Changelog

## 0.0.1 — 2026-08-23

The first tag. 406 commits, 2026-07-28 to 2026-08-23 — everything Phase 1 and
most of Phase 2 in [CLAUDE.md §7](CLAUDE.md). It runs a household's library on
one Mac and is watched on a desktop and an iPhone daily; it has never been run
anywhere else, which is the honest measure of how finished it is.

### Getting video in

- **Paste a link, follow a channel, or curate `topics.yaml`.** The catalogue is
  asked by id first, so a link to something already here costs no request.
- **An hourly scan over every source**, plus a **five-minute RSS pass** for
  subscribed channels only — an hour is the whole of how late a followed
  channel's upload can be, and RSS is cheap enough to ask that often.
- **A metadata backfill on a timer**, one thread, 4 s apart, 200 a pass, giving
  up after 15 consecutive failures. YouTube blocks by address, not by endpoint.
- **A download queue with one worker**, `SELECT … FOR UPDATE SKIP LOCKED`,
  cancel that reaches the process, and retries that wait 2 → 10 → 30 minutes
  rather than arguing with a refusal.
- **Videos upstream will not hand over** are recorded as such and never retried
  automatically — members-only, private, removed. A temporary refusal is
  deliberately never recorded as a permanent one.

### Watching

- **It plays before it has downloaded.** Three tiers — the local file, HLS, and
  a stream muxed from YouTube's own adaptive tracks — and the player opens on
  the best one the browser can play, then climbs to the file when it lands, a
  median of thirteen seconds later.
- **Live broadcasts are their own tier**, with the hour of rewind YouTube's own
  window gives, a LIVE button rather than two numbers that are always wrong, and
  a Live chip that is absent when nothing is on air.
- **Two `<video>` layers** with a handover measured against the viewer's own
  playhead, so a climb never lands behind them or seeks a stream that cannot be
  seeked.
- **A ten-band equaliser and four rooms**, both layers through one Web Audio
  graph, per device — an equaliser corrects for the speakers.
- **Subtitles translated in the page** and, optionally, read aloud, through any
  service that speaks OpenAI's API. The translation is written back as a
  selectable track.
- **Picture in picture, fullscreen, a miniplayer that survives navigation**, and
  a watch screen that is a layer over the tab underneath on a phone.

### The feed

- **Heuristic ranking, no ML** — every score explained component by component at
  `GET /api/feed/explain`.
- **A feed mix you set** with three sliders, and a bucket count so you can see
  what each share can actually draw on.
- **Channel rotation**: heat, revival, an ignored penalty, and a diversity rule
  that keeps a channel from taking two slots in a row.
- **Uninvited videos must be in a language the household watches** — learned
  from history, never configured, and off entirely on a young library.
- **Seven ranking constants on a settings screen**, clamped by the ranker rather
  than trusted.

### The library

- **A storage budget and an eviction sweep** — least recently used, unpinned,
  metadata and thumbnail kept, "Removed — press to fetch again".
- **Save is a personal shelf; pinning is a fact about the disk.** One member
  saving a video keeps its bytes for everybody without putting it on anybody
  else's shelf.
- **Search asks YouTube alongside the library**, split into two sections.
- **History, Watch later, playlists**, the last two a read-only mirror of the
  member's YouTube account.

### The household

- **Profiles, not logins** — two to five people on a trusted LAN. Subscriptions,
  history and recommendations are per person; the videos are the household's.
- **A YouTube account per member**, cookies held as a file at mode 0600 and
  never in the database, write-only across the API. Two authentication failures
  retire an account rather than replaying a dead session hourly.

### The app itself

- **English and Tiếng Việt**, per device, with three guard layers that fail the
  build on a string nobody translated.
- **Four services, a gateway and a log viewer** — ConnectRPC and protobuf
  inside, REST only at the edge, one Postgres schema and one role per service.
- **`logview` on :8184** reads every service's log as one page, including the
  browser's own since this release.

### Known limits

- No `identity` service: the gateway reads `X-User-Id` and anything on the LAN
  can claim to be anyone. That is §6b's decision, not an oversight.
- No ABR yet — Phase 2's 720p rendition is not fetched alongside the 1080p one.
- No TV UI, no auto-follow, no native app.
- iOS: HLS never reaches Web Audio, so the equaliser is off for the seconds
  before a download lands, and background playback is impossible from the web.

---

## 0.0.1 — Tiếng Việt

Bản tag đầu tiên. 406 commit, từ 28/07/2026 đến 23/08/2026. Chạy thư viện của
một nhà trên một máy Mac, xem hằng ngày trên desktop và iPhone — và chưa từng
chạy ở đâu khác, đó là thước đo thật cho mức độ hoàn thiện.

- **Nạp video**: dán link, theo dõi kênh, hoặc khai trong `topics.yaml`. Quét
  mỗi giờ, thêm một lượt RSS 5 phút cho kênh đã đăng ký. Hàng chờ tải một luồng,
  huỷ được, thử lại có giãn cách. Video YouTube từ chối đưa được ghi nhận riêng.
- **Xem**: phát trước khi tải xong — file trên đĩa, HLS, hoặc luồng ghép từ
  chính track của YouTube; trung vị 13 giây là có file. Phát được cả buổi phát
  trực tiếp, tua lại được trong cửa sổ một tiếng của YouTube.
- **Âm thanh**: equalizer 10 dải và 4 kiểu phòng, lưu theo từng thiết bị.
- **Phụ đề**: dịch ngay trong trang và đọc thành tiếng, qua bất kỳ dịch vụ nào
  nói được API của OpenAI.
- **Trang chủ**: xếp hạng theo quy tắc, không ML, giải thích được từng thành
  phần điểm. Ba thanh trượt chia trang, có xoay vòng kênh, và lọc theo ngôn ngữ
  mà nhà này thật sự xem — học từ lịch sử chứ không phải cấu hình.
- **Ổ đĩa**: có hạn mức và lượt dọn tự động. Giữ file là chuyện của cả nhà, còn
  Lưu là kệ riêng của từng người.
- **Nhiều người dùng**: hồ sơ riêng chứ không phải đăng nhập. Mỗi người gắn được
  tài khoản YouTube của mình để lấy về kênh đăng ký, video đã thích và playlist.
- **Hai ngôn ngữ**, English và Tiếng Việt, có ba lớp kiểm tra chặn build nếu còn
  chuỗi chưa dịch.

**Chưa có**: ABR, giao diện cho TV, app native. Trên iOS, HLS không đi qua Web
Audio nên equalizer tắt trong lúc chờ tải, và web không phát nền được.
