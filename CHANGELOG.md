# Changelog

## 0.0.3 — 2026-08-28

One fault, chased to the bottom: **subtitles stopped arriving**. It turned out
to be three separate things wearing the same symptom, and the last of them
cannot be fixed from here at all.

### What was actually wrong

- **YouTube rate-limits the caption endpoint by address**, separately from
  everything else. Video bytes come from googlevideo on signed URLs and were
  never affected — which is exactly why videos kept playing while captions did
  not, and why this took so long to see. Measured on 2026-08-27 it refused this
  address for **thirteen hours straight**: 21 attempts, not one through.
- **The app was spending four requests per video** where one would do — two
  passes (authored and automatic) times two languages — so it reached the limit
  four times faster than it needed to.
- **A hover answered the question pressing play asks.** `?prefetch=1`
  deliberately fetches no captions and queues no transfer, and the prefetch
  wrote its answer under the player's own cache key. Hovering a card and then
  opening it meant the real request was never issued: measured on one video,
  every `/stream` request the gateway had ever seen for it carried
  `prefetch=true` and its folder was never created.
- **A refusal left no trace and was never retried.** `_, _ = cmd.Run(...)`, so
  "upstream said no" and "this video has no captions" were the same empty
  folder and the same silence.

### What changed

- **One request instead of four.** One player call lists what exists, one
  download takes the best of it — Vietnamese if YouTube has it, since its own
  translation beats the one this app would make, English otherwise.
- **A refusal is written down and asked about again**, on the worker's existing
  sweep: 1 min → 5 → 20 → 60 → 3 h → 6 h, and the last step repeats rather than
  giving up. It did give up after four tries at first; the block outlasted that
  by eleven hours and five videos were abandoned mid-outage.
- **Leaving the watch page stops the asking.** A video opened by mistake and
  left after three seconds was being asked about for ever.
- **The caption path says what it is doing**, always — `outcome=landed`,
  `refused` or `none`, with the languages that landed. Every failure had a line
  and success had none, which makes "is this working" a question about the disk
  rather than about the log.
- **A second machine can be asked**, configured in Settings → Transcript beside
  Translation. It sits between the local path and yt-dlp because it answers the
  one failure neither can: both of those leave by the same front door.
  `docs/transcript-server` carries a small server that answers it and a Pyscript
  file that starts it on a Home Assistant box.

### What is still not fixed, honestly

The block is on the **public address**. A second machine in the same house
shares it — measured, and refused with the same 429 in the same minute. That
server helps only through a rotating residential proxy, or on a different
connection entirely; it says which at startup rather than looking like it is
helping. Everything above makes the app reach the limit far more slowly and
recover on its own when a block lifts. None of it lifts a block.

Requires `services/ingest/migrations/0005_subtitle_retries.sql`.

## 0.0.2 — 2026-08-24

A day of faults found on the phone, and one change of how this is run at all.
Every fix below was measured before it was made; three of them were
instrumented first, because the fault produced no evidence and a label is not
evidence.

### The stack starts itself

- **A LaunchAgent** (`scripts/install-agent.sh`), so the Mac coming back does
  not need a terminal. It waits for `/Volumes/Data2` to mount and for Postgres
  to answer before starting anything — a stack started against an unmounted
  media root is [§8 risk 1](CLAUDE.md) happening on every reboot, quietly.
  A login agent, not a daemon: this needs `$HOME` and a mounted volume, so it
  starts on **login**, and a machine expected to return on its own needs
  auto-login too.
- **The gateway serves the built bundle** at `/`, beside `/api` and `/media`,
  and nothing runs on `:5173` any more. Vite is a development server; the
  household is not developing. The app is now at **`:8180`**, one origin.
- Caddy is still §3's answer and is still not installed. What was in the way
  was not TLS — it was serving the library through a dev server.

### The home indicator was taking a band it does not need

`env(safe-area-inset-bottom)` is 34pt, and the tab bar reserved all of it below
its labels: most of a second tab bar of empty space, which reads as a gap under
the app rather than as clearance. The indicator is a line the system draws
*over* what is beneath it.

- The bar is 3.5rem flat, the page reserves 3.5rem, and the **miniplayer rests
  on the bar** instead of hovering a home indicator's height above it with the
  page scrolling past in the gap. On a screen with no tab bar it is one bar
  tall rather than a bar plus a band of bar-coloured nothing.
- **Rotating no longer drops the miniplayer under the tab bar.** Safari answers
  `innerHeight` mid-rotation with a figure that is not yet true and does not
  always correct it, so the viewport is measured three times: now, next frame,
  and once the animation has finished.

### Narration

- **"Speech not started" over a video that was speaking.** The status was
  honest — the pre-generation sweep really had not begun — and it had been told
  there were no cues by two different paths, on a video with 121 of them.
  Cancelling the translation pass woke every waiter with a literal empty list;
  and the sweep, whose effect is declared above the one that fetches cues, was
  told "nobody is fetching" every single time it asked.
- **Narration went silent after switching apps and could not be brought back.**
  The `AudioContext` reports `running` with its clock stopped — measured at
  53.0 seconds for two minutes of playback — so every clip is scheduled behind
  a mark that never arrives and dropped as late. `resume()` cannot help a
  context that believes it is running, and neither can the toggle; only a new
  context has a running clock, which is why reloading was the only remedy.
  The graph now notices its own clock has died and rebuilds itself, re-routing
  both video layers and re-applying the equaliser and the room.
- The sweep, the skipped cues, the audio state and the viewport all say what
  they are doing in the server log now. Three of the faults above were invisible
  for days because the working case and the broken case produced exactly the
  same evidence: none.

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
