# Local YouTube — Project Charter

> Kết quả của phiên grilling ngày 2026-07-28. Đây là nguồn sự thật cho mọi quyết định kiến trúc.
> Nếu một quyết định mới mâu thuẫn với file này, phải cập nhật file này chứ không được lặng lẽ đi hướng khác.

## 1. Bản chất hệ thống

Media library **tự host** chạy trên Mac M4 tại nhà. `yt-dlp` là **công cụ nhập liệu** (ingest), **không phải** proxy YouTube realtime — video được tải về đĩa một lần rồi phục vụ từ LAN.

- 2–5 user trong nhà (multi-user nhẹ, không đăng ký công khai)
- Đích cuối: xem trên **browser Smart TV**; **Phase 1 làm web desktop trước**
- Reference layout: `Example/home.png`, `Example/play.png` (YouTube desktop)

## 2. Ràng buộc cứng (đã xác minh trên máy)

| | |
|---|---|
| Disk | **34 GiB free** → ngân sách media ~25 GiB → **~150 video @1080p** |
| Máy | Apple M4, 10 core |
| Đã cài | `ffmpeg`, `yt-dlp`, `go`, `node`, `python3` |
| Chưa có | `docker`, `postgres`, `redis` (Postgres cần cài qua Homebrew) |
| Mạng | Mac phải luôn bật, cần IP tĩnh LAN + HTTPS (cert nội bộ) để TV phát được |

## 3. Kiến trúc

| Hạng mục | Quyết định |
|---|---|
| Mô hình | Self-host library (KHÔNG proxy stream từ YouTube) |
| Topology | **Microservices thật**: `identity` · `catalog` · `ingest` · `recsys` + **API Gateway** |
| Ngôn ngữ | **Go toàn bộ**, dùng [`lrstanley/go-ytdlp`](https://github.com/lrstanley/go-ytdlp) |
| Transport | **ConnectRPC + protobuf** (`buf` codegen). Gateway là nơi **duy nhất** nói REST/JSON ra ngoài |
| DB | **1 Postgres instance**, mỗi service **1 schema + 1 DB user riêng** |
| Queue | Postgres table + `SELECT … FOR UPDATE SKIP LOCKED` |
| Serving | **Caddy** reverse proxy: TLS + serve `/media` static; proxy phần còn lại vào gateway |
| Media URL | Không bảo vệ (tin cậy LAN) |
| CDN | **Ngoài scope** — vô nghĩa trên LAN |

### Luật bất di bất dịch
1. **Không service nào được query DB của service khác.** Ranh giới do DB permission ép, không do lời hứa. Vi phạm = distributed monolith.
2. **Clean architecture = hướng phụ thuộc**, không phải số lượng process. `domain` không import DB/HTTP/framework.
3. Ingest worker chạy **process riêng** (ffmpeg/yt-dlp blocking, chạy chung sẽ đơ API).

## 4. Media pipeline

**Phase 1 (hybrid — chốt lại ngày 2026-07-28):** video chưa có trên đĩa vẫn xem được ngay.
`GET /api/videos/{id}/stream` trả `local` (file trên đĩa) hoặc `upstream` (URL yt-dlp resolve,
ngắn hạn) trong lúc job tải nền chạy. Tải xong thì endpoint tự đổi sang `local`.
Không transcode, không HLS. Player = `<video>` trần.

> **Giới hạn đã biết:** chỉ progressive (muxed) format mới phát thẳng được, nên lần xem đầu
> qua upstream thường thấp hơn 1080p. Player có nhãn báo rõ.

**Phase 2 (nâng cấp rẻ):** tải thêm rendition 720p **có sẵn của YouTube** + remux `-c copy` sang HLS → ABR thật với CPU ≈ 0.
> Ghi nhớ: **"không transcode" ≠ "không ABR"**. Cái đắt là *encode lại*; YouTube đã encode sẵn nhiều mức.

### Eviction
Mỗi video có `last_accessed_at` + `pinned`. Vượt ngưỡng ~22 GiB → xoá **file media** của LRU không pinned, **giữ metadata + thumbnail + history** → UI hiện "Đã gỡ — bấm để tải lại", re-ingest 1 click.

## 4b. Code conventions

- **All source code, identifiers, comments, commit messages and in-app UI copy MUST be in English.**
  Vietnamese is allowed only as *content data* (e.g. a video title that is genuinely Vietnamese).
  Chat/discussion happens in Vietnamese; the artifacts do not.
- Go: standard layout per service — `cmd/`, `internal/domain`, `internal/usecase`, `internal/adapter`.
- Frontend: feature-sliced (see §5). No `fetch` inside `ui/`.

## 5. Frontend

**Vite + React + TypeScript + Tailwind thuần + TanStack Query.**

- **KHÔNG dùng shadcn/ui** — clone pixel-perfect thì component library chỉ làm phải ghi đè. Tự dựng component theo design token trích từ `Example/*.png`.
- Dùng skill `ui-ux-pro-max` khi thiết kế/xây UI.

### Cấu trúc feature-sliced
```
src/features/<feature>/
  domain/          # entity + type thuần, KHÔNG import React
  application/     # use-case: hook gọi repository, KHÔNG biết HTTP
  infrastructure/  # repository impl: gọi gateway REST
  ui/              # component
```
`ui/` không bao giờ gọi `fetch` trực tiếp. Nhờ vậy khi làm `/tv` (Phase 3) chỉ phải viết lại **tầng `ui/`**.

### Nguyên tắc UI: KHÔNG RENDER NÚT CHẾT
Mỗi phần tử hoặc có chức năng thật, hoặc bị bỏ.

| YouTube gốc | Trong hệ này |
|---|---|
| Create (+) | → **"Add video"** (entry point ingest) |
| Notification + badge | → **sự kiện ingest** ("3 video đã tải xong", "1 lỗi") |
| Downloads | → **Storage** (dung lượng, pinned, sắp bị evict) |
| Explore / chip filter | → đổ từ **tag & category thật** trong catalog |
| Your videos, YT Music, YT Kids, footer, Shorts, Live | **BỎ** |
| Subscribe | Bookmark kênh (P1) → thật khi có auto-follow (P3) |
| Share | Copy link LAN vào clipboard |

## 6. Recommendation

**Heuristic, KHÔNG ML.** Lý do: ~150 video và 5 user → collaborative filtering ra rác, embedding không hơn tag-matching mà lại không debug được.

- **P1:** "mới thêm" + "xem tiếp" + "chưa xem"
- **P2:** grid trộn có chủ ý ~30% chưa xem / 25% mới / 20% kênh theo dõi / 15% xem dở / 10% xem lại; chống lặp impression 24h
- **`Next` (watch page):** cùng kênh > cùng tag > affinity chung; loại video vừa xem

> **Giới hạn đã ghi nhận:** với ~150 video tự tay import, recommendation chỉ là *sắp xếp lại tủ đồ của chính mình* — không tạo được cảm giác khám phá của YouTube. Muốn có, phải làm feed kéo video ngoài vào (Phase 3).

## 7. Phạm vi

### Phase 1 — vòng lặp lõi
**Có:** ingest 1 URL → xem được · Home (grid 3 cột) · Watch (player + info + sidebar Next) · login seed 2 tài khoản (chưa có màn đăng ký) · search full-text

**Chưa có:** comment · transcript panel · history/like/watch-later · chip filter động · ABR/hls.js · import playlist/channel · "Ask" (đã cắt)

### Phase 2
comment lồng nhau · transcript click-to-seek · history/like/playlist · recsys trộn · notification · Storage page + eviction · import playlist/channel · nâng lên ABR

### Phase 3
auto-follow kênh (subscribe thành thật) · UI `/tv` điều khiển D-pad · app mobile · *(tùy chọn)* feed kéo video ngoài

### Đã cắt dứt khoát
- **CDN** — vô nghĩa trên LAN
- **"Ask" AI** — cắt khỏi P1. Nếu làm lại, ưu tiên full-text search trong transcript (không LLM) hơn là nhét model 5GB vào ổ đĩa đã chật
- **144p / 4K** — không ai xem 144p; 4K giết disk
- **Flutter Web** — canvas render, không clone pixel-perfect được

## 8. Rủi ro đã biết

1. **34 GiB là chỗ đau nhất.** 150 video là ít; eviction sẽ chạy sớm hơn dự đoán. **Một SSD ngoài sẽ giải phóng toàn bộ ràng buộc này** — nếu có, xem lại mục 4.
2. **Microservices + gRPC với người chưa từng làm gRPC** → P1 chậm hơn monolith đáng kể, thời gian đầu chủ yếu là setup. Đã chấp nhận với giá đã biết. Dùng **ConnectRPC** (curl debug được từng service) thay vì gRPC thuần.
3. **HTTPS trên Smart TV chưa được chứng minh.** Phải thử sớm với TV thật, đừng để tới Phase 3.
4. **yt-dlp hỏng định kỳ** khi YouTube đổi cơ chế → ingest phải xử lý lỗi tử tế + cho retry.

## 8b. Build status (cập nhật 2026-07-28)

### Chạy được, đã verify bằng request thật

**Hạ tầng:** 4 service (`catalog` 8081 · `recsys` 8082 · `ingest` 8083) + `gateway` 8080 + web 5173.
Postgres 17, mỗi service 1 schema + 1 role riêng. ConnectRPC nội bộ, REST ra ngoài.
`scripts/dev.sh` chạy cả stack. `make check` = buf lint + tsc + go build.

**Nội dung:** `topics.yaml` là nguồn duy nhất của feed. Scanner quét mỗi 12 tiếng
(`POST /api/topics/refresh` để quét ngay). Hiện **280 video / 6 chủ đề / 7 nguồn**,
quét hết trong ~8 giây, chỉ lấy metadata.

**Phát:** bấm play → `GET /api/videos/{id}/stream` trả `local` hoặc `upstream`,
đồng thời **tự enqueue tải nền**. Tải xong thì client tự đổi sang bản local.
Player: autoplay (bị chặn thì muted + nút bật tiếng), click = play/pause,
`Space`/`←`/`→`/`m`, volume slider, buffered range thật, phụ đề (en/vi) với menu CC — phụ đề
được tải **trước** file video nên xem được ngay trong lúc còn phát upstream. Hết video thì
đếm ngược 5 giây rồi phát video kế (có công tắc Autoplay, tự dừng sau 3 video không ai tương tác).

**Search:** **luôn** hỏi YouTube song song với thư viện. Trang kết quả 2 khối
(In your library / On YouTube). Autocomplete từ local (chủ đề → kênh → tiêu đề).
Bỏ dấu tiếng Việt hai chiều qua `unaccent`. Mở video từ YouTube → ghi metadata
**không gán chủ đề**, feed vẫn do topics.yaml quyết định.

**Phân trang:** feed + search dùng `useInfiniteQuery`, tự nạp trước 600px, kèm nút
"Load more" thật cho bàn phím/remote.

**Activity:** trang `/activity` gộp hàng đợi tải (kèm lỗi yt-dlp nguyên văn) và kết quả
lần quét gần nhất (kèm nguồn nào hỏng). Có nút "Scan now" thật. Không phải log viewer —
log 4 service vẫn ra stdout.

**Feed vô tận:** `GetFeedPage` đông cứng thứ tự rank vào một snapshot theo phiên (memory
recsys, TTL 30 phút) — trang sau đọc từ snapshot đó thay vì rank lại, nên không trùng video.
Khi snapshot còn dưới 48 video, gateway tự gọi `ExpandLibrary` (ingest) chạy nền: đào sâu
source trong topics.yaml (có cursor lưu Postgres để lần sau tiếp tục từ chỗ cũ) → related qua
InnerTube (`/youtubei/v1/next`, tự viết, không có contract) → search theo tên chủ đề. Chỉ một
lượt expand chạy cùng lúc.

**Eviction:** catalog chạy sweep mỗi giờ (`services/catalog/internal/usecase/evict.go`).
Vượt 20 GiB → xoá file media của LRU không pinned về 16 GiB, giữ metadata + thumbnail +
history. Ngưỡng chỉnh qua `EVICTION_HIGH_BYTES`/`EVICTION_LOW_BYTES`.

### Chưa làm — thứ tự đề xuất khi làm tiếp

1. **Serve-while-downloading (B1, 720p)** — phần khó nhất còn lại. Hiện bấm play vẫn đi
   đường upstream (~360p) rồi tải nền 1080p. Cần: gateway serve file **đang được ghi**,
   trả 206 cho phần đã có, chặn seek quá mép buffer. Đổi `DEFAULT_HEIGHT` về 720 và ép
   yt-dlp lấy progressive format.
2. **Recsys trộn 55/10/20/15** — hiện mới có affinity theo **kênh**, chưa có affinity theo
   **chủ đề** và chưa có tỉ lệ khám phá cố định (chống buồng vọng).
3. **3 trang còn thiếu**: `/history` · `/saved` · `/storage`. API đã có sẵn
   (`ListHistory`, `GetStorageUsage`, `SetPinned`) — chỉ thiếu tầng `ui/`.
   **Đang là link chết trong sidebar.**

### Quyết định đã bị đảo trong quá trình làm

- **Search**: từng chốt "chỉ tìm local" (Câu 3/12) → **đảo lại**: search luôn hỏi YouTube.
  Lý do: feed là thứ được phục vụ, search là thứ chủ động đi tìm — không có lý do bó search
  trong nguồn của feed.
- **Playlist**: từng định làm bảng `playlists` + watch-later → **bỏ hẳn**. Chủ đề thay thế,
  "Keep" (pin) là bộ sưu tập cá nhân duy nhất.
- **`categories` → `topics`**: YouTube chỉ có ~15 category toàn cục, vô dụng để phân loại.
- **Feed**: từng chốt "topics.yaml là nguồn duy nhất của feed" → **đảo lại**: khi feed sắp cạn,
  gateway gọi `ExpandLibrary` để kéo thêm — đào sâu chính các source trong topics.yaml trước,
  rồi related qua InnerTube, cuối cùng mới là search. Lý do: cuộn vô tận là yêu cầu, mà 280 video
  thì hết sau ~12 trang. Thứ tự các lớp là có chủ đích — lớp đào sâu không thể hỏng, nên
  InnerTube vỡ thì feed vẫn vô tận, chỉ kém đa dạng.
- **Phân trang feed**: offset trên bảng xếp hạng vừa rank lại → **snapshot đông cứng theo phiên**
  (memory recsys, TTL 30 phút). Lý do: `recordImpressions` trừ điểm chính những video vừa hiện,
  nên trang sau rank trên bảng đã khác trang trước và sinh ra video trùng.

### Bẫy đã gặp, đừng lặp lại

- **Lỗi phụ đề từng giết cả video**: gộp `--write-subs` vào lệnh tải → 429 ở endpoint caption
  làm yt-dlp exit 1 → mất video đã tải xong. Giờ tách lượt riêng, không được gộp lại.
- **`--flat-playlist` thiếu rất nhiều field**: không có channel per-entry (dùng `playlist_uploader`),
  không có view count, không có ngày đăng, không có `thumbnail` (dùng mảng `thumbnails`).
  **Không được default ngày đăng = now** — nó hiện thành "1 minute ago" trên mọi card.
- **yt-dlp đặt cùng tên file cho phụ đề người làm và máy làm** → phải chạy 2 lượt mới phân biệt được.
- **`ffmpeg` nuốt stdin** trong vòng lặp bash → luôn dùng `-nostdin`.
- **pgx encode nil slice thành NULL** → vi phạm NOT NULL của cột mảng.

## 9. Câu còn để ngỏ
- TV nhà là hãng gì? (ảnh hưởng cách xử lý cert)
- Có SSD ngoài không? (đổi toàn bộ bài toán disk)
