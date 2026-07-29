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
| Disk | ~~34 GiB free~~ **đã giải quyết (2026-07-28): SSD ngoài `/Volumes/Data2/Youtube`, 437 GiB trống.** `MEDIA_ROOT`/`STORAGE_BUDGET_BYTES`/`EVICTION_HIGH_BYTES`/`EVICTION_LOW_BYTES` trỏ ra đó qua `scripts/dev.sh` (budget 300 GiB, sweep 350→300 GiB). Ổ trong máy không còn là ràng buộc cứng cho dev — vẫn là ràng buộc thật nếu deploy sang máy khác không có ổ ngoài. |
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

**Phase 1 (hybrid — chốt lại lần 3 ngày 2026-07-29):** video chưa có trên đĩa vẫn xem được ngay.
`GET /api/videos/{id}/stream` **liệt kê** mọi nguồn phát được lúc này thay vì chọn hộ:

| Nguồn | Là gì | Seek |
|---|---|---|
| `local` | file trên đĩa. Có thì không cần nguồn nào khác | ✅ |
| `instant` | URL progressive thô của YouTube (itag 18, 360p), browser tự range-request | ✅ |
| `remux` | mux trực tiếp 2 luồng adaptive → fMP4. Chỉ dùng khi video **không có** progressive | ❌ |

Player leo tầng: `instant` phát trong ~17ms → đổi sang `local` khi tải xong.
Không transcode, không HLS.

`?prefetch=1` = mới rê chuột lên card, chưa bấm play: resolve và cache URL để lần bấm sau
tức thì, **không** xếp hàng tải. Thiếu vạch này thì lướt feed sẽ làm đầy ổ đĩa có trần cứng.

> **Đánh đổi cố ý:** lần xem đầu là 360p. Đó là giá của "phát ngay + seek được ngay", và nó
> chỉ kéo dài vài giây (xem số đo ở §8b). Player có nhãn báo rõ đang ở tầng nào.

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
| Subscribe | **Thật ngay ở P1**: thêm kênh thành nguồn ingest động, scanner quét như mọi source |
| Share | Copy link LAN vào clipboard |

## 6. Recommendation

**Heuristic, KHÔNG ML.** Lý do: ~150 video và 5 user → collaborative filtering ra rác, embedding không hơn tag-matching mà lại không debug được.

- **P1 (đã làm):** grid trộn 30% chưa xem / 25% mới / 20% kênh theo dõi / 15% xem dở /
  10% xem lại; chống lặp impression 24h
- **Tín hiệu chấm điểm (2026-07-29)** — tất cả đều tất định, **không train gì cả**:

  | Tín hiệu | Cách tính | Trọng số |
  |---|---|---|
  | Đang xem dở | `0.02 < fraction <= 0.95` | +3.0 |
  | Chưa từng mở | video **không có** trong `WatchedFraction` | +1.5 |
  | **Mở rồi bỏ ngay** | có trong map, `fraction <= 0.02` | **−2.5** |
  | Kênh theo dõi | | +1.2 |
  | Mới thêm | `exp(-days/14)` | ×1.0 |
  | Affinity kênh (từ **xem**) | Σ fraction theo kênh, chuẩn hoá 0..1 | ×1.0 |
  | Affinity chủ đề (từ **xem**) | Σ fraction theo topic, chuẩn hoá 0..1 | ×1.0 |
  | Affinity từ **like** | topic 1.0 / kênh 0.8 / hashtag 0.5 | ×2.0 |
  | **Giữ chân toàn cục** | `avg(max(fraction) per viewer)` | ×1.5 |
  | Đã hiện trong 24h | | −2.0 |

  Ba điểm đáng ghi:
  - **`BOUNCED` là một Reason riêng.** Trước đây `fraction <= 0.02` rơi vào nhánh
    `default` và được cộng +1.5 y như chưa xem — tức là **cách chắc chắn nhất để giữ
    một video trong feed là từ chối nó**. Phân biệt bằng comma-ok, không phải giá trị 0.
  - **Affinity đọc từ lịch sử xem, không chỉ từ Like.** Thư viện này có 9 like trên
    2.045 tín hiệu xem; đọc sở thích từ Like thôi là gần như không đọc được gì.
  - **Vừa xem xong ≠ đã từng xem.** `WatchedFraction` chỉ nói *có từng xem chưa*, không
    nói *khi nào*. Up-next cần đúng cái "vừa nãy": hai video **cùng kênh + cùng chủ đề**
    là gợi ý mạnh nhất của nhau (`weightSameChannel` 2.5 + `weightSharedTags` 1.5), nên
    bấm Next hai lần là quay lại chỗ cũ — **vòng lặp 2 bài không thoát được**, đã gặp thật.
    Thêm `RecentlyWatched` (cửa sổ **3 tiếng**) với `penaltyRecentlyWatched = 8.0` —
    **cố ý lớn hơn tổng hai trọng số kia**, vì nhỏ hơn thì không gãy được vòng.
    Sửa ở **server**, không phải ở client: bản trước dựa vào trail trong sessionStorage
    của trình duyệt, mà một cái vòng lặp cấu trúc thì không nên phụ thuộc vào lưu trữ
    phía client có hoạt động hay không.
  - **Hạn ngạch theo lý do KHÔNG chống được một kênh chiếm cả trang.** Đo thật:
    44% thời lượng xem dồn vào một kênh → affinity chuẩn hoá thành 1.0 trong khi kênh
    kế chỉ 0.23 → **23/24 video trang đầu là một kênh**, mà mọi hạn ngạch vẫn "đạt".
    Lý do: video của kênh đó **đồng thời** là chưa-xem, mới-thêm và kênh-theo-dõi, nên
    nó lấp mọi rổ cùng lúc. Phải chặn theo **trục khác**: `applyChannelDiversity`, tối đa
    **3 video/kênh trong mỗi cửa sổ 24**. Sau khi chặn: kênh áp đảo còn 12%, số kênh trên
    trang đầu từ 2 → 10. Vẫn chỉ sắp xếp lại, không bỏ video nào.
  - **Giữ chân tính trong recsys**, không hỏi catalog: catalog không biết ai xem bao
    lâu, và ranh giới đó là thứ giữ cho đây là nhiều service chứ không phải một chương
    trình chạy trong bốn process. Phải lấy `max` theo từng người xem **trước** rồi mới
    trung bình — tín hiệu WATCH được ghi theo nhịp trong lúc phát, nên trung bình thẳng
    trên các dòng sẽ đo "thời điểm trung bình ta lấy mẫu", tức khoảng nửa video bất kể
    hay dở.
- **Like:** cộng affinity theo topic 1.0 / kênh 0.8 / hashtag 0.5, cộng dồn qua từng like.
  Đưa lên P1 vì like mà không đổi gì thì là nút chết.
- **Dislike**: loại khỏi feed và up-next, **vẫn tìm được qua search và trang kênh**.
  Không phải tính năng phải làm — nó đúng sẵn nhờ ranh giới service: catalog không
  nhìn thấy signal của recsys nên không thể lọc theo nó.
- **`Next` (watch page) — ĐẢO 2026-07-29:** trước là *cùng kênh > cùng tag*. Giờ là
  **cùng thể loại là chính, kênh chỉ là một cách chia sẻ thể loại đó, không phải cách tốt
  hơn**: `weightSameChannel = weightSharedTags = 2.5`, cộng **theo từng tag trùng** nên
  hai video trùng cả topic lẫn hashtag thắng một video chỉ trùng kênh.
  Kèm **trần cứng 3 video/kênh** trong rail (`capPerChannel`).
  Lý do đảo: giữ nguyên thứ tự cũ thì rail thành **20/20 một kênh** — đúng chủ đề nhưng
  là ngõ cụt. Đo sau khi đảo: video *Entertainment* → **20/20 Entertainment qua 9 kênh**;
  video *Music* → **20/20 Music qua 10 kênh**.
  `capPerChannel` khác `applyChannelDiversity` (feed) ở chỗ **trần là tuyệt đối**, không
  dồn phần bị giữ xuống cuối — với rail 20 phần tử thì "dồn xuống cuối" vẫn rơi vào trong
  trang, và kênh bị chặn lặng lẽ chiếm 8 slot thay vì 3.
  **Thứ tự đó phải được ép bằng trọng số, không phải bằng lời hứa (2026-07-29).** Đo thật:
  xem một video *Entertainment* của kênh game → **20/20 gợi ý là nhạc SOOBIN**, không cùng
  kênh cũng không cùng chủ đề. Ba nguyên nhân cộng lại, mỗi cái tự nó vô hại:
  - `TopicScore` **cộng dồn** qua các chủ đề của một video → video gắn cả `Music` lẫn
    `Vietnamese music` ăn affinity **hai lần**. Sửa: lấy **chủ đề khớp mạnh nhất**, không cộng.
  - `weightContinueWatching` (+3.0) ở up-next **lớn hơn cùng-kênh + cùng-chủ-đề** (2.5+1.5).
    "Xem dở" trả lời câu *"tôi nên xem gì"*, không phải *"cái này tiếp theo là gì"*.
  - `weightRetention` với **1 user** chính là lịch sử của user đó → đếm sở thích lần hai.

  Sửa chung: ở **up-next**, mọi thứ **không phải quan hệ với video đang xem** (affinity kênh,
  affinity chủ đề, retention, continue-watching) bị nhân `upNextTasteDamping = 0.35`.
  Quan hệ đứng đầu, sở thích chỉ phá hoà. Feed **không** damp — ở đó sở thích *nên* thắng.
  Đánh đổi đã biết: up-next giờ gần như toàn cùng một kênh, đúng như charter chốt.

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

1. ~~34 GiB là chỗ đau nhất~~ **Đã giải quyết bằng SSD ngoài** (xem mục 2). Rủi ro còn lại: ổ ngoài rớt kết nối thì service ghi lỗi vào file trên đường dẫn không tồn tại — chưa có test cho trường hợp này.
2. **Microservices + gRPC với người chưa từng làm gRPC** → P1 chậm hơn monolith đáng kể, thời gian đầu chủ yếu là setup. Đã chấp nhận với giá đã biết. Dùng **ConnectRPC** (curl debug được từng service) thay vì gRPC thuần.
3. **HTTPS trên Smart TV chưa được chứng minh.** Phải thử sớm với TV thật, đừng để tới Phase 3.
4. **yt-dlp hỏng định kỳ** khi YouTube đổi cơ chế → ingest phải xử lý lỗi tử tế + cho retry.
5. **YouTube chặn theo IP nếu bắn quá nhiều full-metadata (ĐÃ XẢY RA 2026-07-29).**
   Backfill topic chạy 8 luồng song song; tới ~800 video thì mọi full fetch trả về
   *"Sign in to confirm you're not a bot"*. **Chặn này không giới hạn trong backfill** —
   nó giết luôn `ResolveStream`, tức là **không phát được video nào chưa tải về đĩa**.
   Flat listing (đường của scanner) **không** bị ảnh hưởng.
   Bài học: full metadata fetch là thao tác **đắt và bị đếm**; flat listing thì không.
   Giờ backfill chạy **1 luồng, cách nhau 4 giây, mặc định 200 video/lượt**, và **tự dừng
   sau 15 lần hỏng liên tiếp** — cố đấm xuyên qua một cái chặn chỉ làm nó dài thêm.

## 8b. Build status (cập nhật 2026-07-28)

### Chạy được, đã verify bằng request thật

**Hạ tầng:** 4 service (`catalog` 8181 · `recsys` 8182 · `ingest` 8183) + `gateway` 8180 + web 5173.
Đổi khỏi block 808x vì máy này có project khác chiếm cứng 8080 và 8082.
Postgres 17, mỗi service 1 schema + 1 role riêng. ConnectRPC nội bộ, REST ra ngoài.
`scripts/dev.sh` chạy cả stack. `make check` = buf lint + tsc + go build.

**Nội dung:** `topics.yaml` là nguồn duy nhất của feed. Scanner quét **mỗi 1 tiếng**
(đổi từ 12 tiếng ngày 2026-07-29; chỉnh qua `SCAN_INTERVAL`, vd `SCAN_INTERVAL=30m`).
Chu kỳ này **chính là** độ tươi tối đa của feed — không gì đăng trên YouTube có thể
xuất hiện ở đây trước khi một lượt quét nhìn thấy nó. Một lượt đi hết 63 nguồn mất
~3 phút và dùng flat listing (rẻ); thứ đắt là fetch metadata từng video, mà scanner
cố ý **không** làm (xem §8b). `POST /api/topics/refresh` để quét ngay. Hiện **280 video / 6 chủ đề / 7 nguồn**,
quét hết trong ~8 giây, chỉ lấy metadata.

**Phát ngay từ giây đầu, seek được ngay (2026-07-29):** bấm play → `/stream` liệt kê nguồn
(§4), player phát `instant` trong ~17ms và **tự enqueue tải nền**; tải xong thì đổi sang
`local`. Rê chuột lên card 250ms → prefetch resolve, nên lúc bấm play thường **không còn
request nào** phải chờ. Cache resolve trong ingest: đo thật **1.85s → 0.008s**.
**Seek luôn bật** trừ khi đang ở tầng `remux`.
**Hai thẻ `<video>`** chồng nhau: nguồn mới nạp và seek sẵn ở thẻ ẩn tại mốc
`hiện tại + 0.6s`, tới mốc thì hoán đổi opacity. Không chớp đen, không tua lùi. Đổi một thẻ
tại chỗ chính là thứ gây chớp trước đây.
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

1. **Playback chặng 2** (chặng 1 xong 2026-07-29): chèn lại tầng `remux` 1080p vào **giữa**
   `instant` và `local` — dựng song song ở thẻ ẩn, lên khi sẵn sàng; seek khi đang ở tầng đó
   = mở lại ffmpeg với `-ss` (chỉ bắn khi **thả tay** khỏi thanh seek, không bắn lúc đang kéo);
   thêm menu **Auto / 1080p / 360p**, trong đó **ghim là lệnh** — chọn tay thì không tự tụt
   tầng, Auto thì hụt hơi là về 360p và không thử lại. Chỉ 3 mục: 480p/720p tốn đúng 2.2s
   ffmpeg như 1080p nên không mua thêm được gì.
2. **3 trang còn thiếu**: `/history` · `/saved` · `/storage`. API đã có sẵn
   (`ListHistory`, `GetStorageUsage`, `SetPinned`) — chỉ thiếu tầng `ui/`.
   **Đang là link chết trong sidebar.**

### Quyết định đã bị đảo trong quá trình làm

- **"Stream không seek được" → SAI, đã đo lại (2026-07-29)**: mục dưới từng kết luận stream
  remux "không seek được cho tới khi file local tải xong". Sai. `ffmpeg -ss 120` trên URL
  adaptive seek bằng HTTP range và ra fragment đầu sau **2.1s** — đắt, không phải bất khả thi.
  Ghi lại để không ai trích lại câu cũ như một ràng buộc vật lý.
- **Số đo nền, máy này, mạng này (2026-07-29)** — mọi quyết định playback phải đối chiếu:
  | | |
  |---|---|
  | `yt-dlp -J` một lần (ra **cả** itag18 lẫn adaptive) | 1.37s |
  | itag 18: TTFB / range | 17ms / `206` — seek native |
  | remux → fragment đầu | 2.2s (chưa kể resolve) |
  | **tải trọn 1080p, video 289s/42MB, cold** | **2.3s** |
  | **tải trọn 1080p, video 850s/67MB, cold** | **7.6s** |
  | đọc tuần tự 1 kết nối | bị bóp còn 3.15 Mbps (yt-dlp không dính) |

  **Hệ quả**: tải xong toàn bộ file còn **nhanh hơn** remux đẻ ra fragment đầu. Nên remux
  bị đẩy xuống làm dự phòng cho video không có progressive, không còn là đường chính.
- **Serve-while-downloading → remux fMP4**: charter từng chốt "gateway serve file **đang được
  ghi**, trả 206 cho phần đã có". **Bất khả thi, đã đo**: tải 1080p là 2 luồng riêng
  (`1080p.f399.mp4` + `1080p.f251.webm`) rồi mới merge — file `1080p.mp4` **không tồn tại**
  cho tới giây cuối. Không có gì để serve.
  **Thay bằng**: ffmpeg remux 2 URL adaptive → **fragmented MP4** đẩy thẳng qua pipe
  (`-movflags frag_keyframe+empty_moov+default_base_moof`). MP4 thường để index ở cuối nên
  chưa xong là chưa phát được; fMP4 phát được từ fragment đầu. `-c copy`, không encode lại,
  CPU ≈ 0 — đúng luật "không transcode" của §4.
  **Giá phải trả**: stream không có index → **không seek được** cho tới khi file local tải xong
  (seek bar bị disable, có nói rõ). Ưu tiên h264 hơn AV1/VP9 vì TV cũ giải mã được h264.
  **Bối cảnh**: YouTube đã bỏ hết progressive độ nét cao — chỉ còn itag 18, tối đa 360p.
  Đó là lý do lần xem đầu trước đây luôn mờ.
- **yt-dlp cũ làm mất adaptive format**: bản 2026.02.04 chỉ thấy itag 18; nâng lên 2026.07.04
  thì đủ 144p→1080p. Đúng rủi ro §8.4 — **kiểm tra version yt-dlp trước khi kết luận
  "YouTube không có định dạng đó"**.

- **Search**: từng chốt "chỉ tìm local" (Câu 3/12) → **đảo lại**: search luôn hỏi YouTube.
  Lý do: feed là thứ được phục vụ, search là thứ chủ động đi tìm — không có lý do bó search
  trong nguồn của feed.
- **Playlist**: từng định làm bảng `playlists` + watch-later → **bỏ hẳn**. Chủ đề thay thế,
  "Keep" (pin) là bộ sưu tập cá nhân duy nhất.
- **`categories` → `topics`, lần 1**: YouTube chỉ có ~15 category toàn cục → chốt bỏ, dùng
  topics.yaml curate tay.
- **`categories` → `topics`, lần 2 (2026-07-28, đảo lại lần 1)**: topic của video lấy từ
  category thật của YouTube (vd "Science & Technology"), giống taxonomy YouTube dùng cho chip
  Subscriptions/Explore. Video cũ giữ nguyên tên topic cũ (Tech, Gaming...) — hai tập tên
  cùng tồn tại trong sidebar.
  **Category lấy ở đâu**: `--flat-playlist` (cách scan) **không** trả category — chỉ full fetch
  mỗi video (`Preview`, ~2.2s) mới có. Nên scan/expand **không bao giờ** gọi Preview; category
  được nhặt **miễn phí** ở hai chỗ vốn đã gọi Preview sẵn: `EnsureVideo` (mở video từ search /
  trang kênh) và worker tải video. Đánh đổi: video chưa ai mở thì chưa có topic.
  **Đã thử cách khác và bỏ**: từng cho scan tự fetch category từng video mới. Đo thật:
  scan 8 giây → 101 giây cho 40 video mới; với 55 kênh subscribe thì thành ~73 phút. Không đáng.
  **Bổ sung 2026-07-29 — không đảo quyết định trên, mà bù cho hệ quả của nó**: vì scan
  không gán topic nên 2.337/3.092 video (¾ thư viện) không có topic, và chúng gần như vô
  hình với chip lọc lẫn với nửa "chủ đề" của affinity. Thêm `BackfillTopics`
  (`POST /api/topics/backfill`, tuỳ chọn `?limit=`): **một lượt riêng, chạy khi được gọi**,
  8 luồng song song. Đo thật: 0,32s/video → ~12 phút cho cả thư viện, so với ~96 phút nếu
  chạy tuần tự. Lượt này tự nối tiếp được vì nó chọn theo "chưa có topic".
  An toàn nhờ chính upsert của catalog: `media_state`, `media_path`, `added_at` **không**
  nằm trong `DO UPDATE SET`, và `topics` thì hợp nhất chứ không thay thế — nên backfill
  không thể hạ cấp video đã tải hay xoá topic do topics.yaml gán.
- **Trang kênh**: từng đọc từ catalog local (`ListChannelVideos`) → **đảo lại**: đọc **live từ
  YouTube**, phân trang theo offset (`ListChannelUploads`). Lý do: scan chỉ mang về ~40 video mới
  nhất, nên trang kênh đọc catalog sẽ bị chặn ở con số đó vì lý do người xem không nhìn thấy.
  Bấm vào video chưa có trong thư viện → `EnsureVideo` ghi metadata rồi mở, đúng luồng của
  kết quả search. Subscribe **không cần đợi scan** mới xem được kênh.
- **Feed**: từng chốt "topics.yaml là nguồn duy nhất của feed" → **đảo lại**: khi feed sắp cạn,
  gateway gọi `ExpandLibrary` để kéo thêm — đào sâu chính các source trong topics.yaml trước,
  rồi related qua InnerTube, cuối cùng mới là search. Lý do: cuộn vô tận là yêu cầu, mà 280 video
  thì hết sau ~12 trang. Thứ tự các lớp là có chủ đích — lớp đào sâu không thể hỏng, nên
  InnerTube vỡ thì feed vẫn vô tận, chỉ kém đa dạng.
- **Nguồn nội dung**: từng chốt "topics.yaml là nguồn duy nhất" → **đảo lại**: có hai nguồn,
  topics.yaml (curate trước, nằm trong git) và subscription (chọn trong lúc dùng, nằm trong DB).
  Cả hai đổ vào cùng scanner. Lý do: subscribe một kênh lạ mà không kéo nội dung về thì nó là
  nút chết — catalog chỉ có 1 video của kênh đó, feed không có gì để đẩy lên.
  **App không bao giờ tự ghi vào topics.yaml** — file đó là của người dùng.
- **Phân trang feed**: offset trên bảng xếp hạng vừa rank lại → **snapshot đông cứng theo phiên**
  (memory recsys, TTL 30 phút). Lý do: `recordImpressions` trừ điểm chính những video vừa hiện,
  nên trang sau rank trên bảng đã khác trang trước và sinh ra video trùng.

### Bẫy đã gặp, đừng lặp lại

- **Lỗi phụ đề từng giết cả video**: gộp `--write-subs` vào lệnh tải → 429 ở endpoint caption
  làm yt-dlp exit 1 → mất video đã tải xong. Giờ tách lượt riêng, không được gộp lại.
- **Nâng chất lượng từng phụ thuộc vào một danh sách bị cắt (sửa 2026-07-29)**: player
  biết "file local đã về" bằng cách tìm job trong `GET /api/ingest/jobs` — danh sách
  **mọi** job, giới hạn 50, sắp theo `created_at DESC`. Một loạt job vừa xong (một lần
  quét, hay vài video mở liên tiếp) đẩy job **đang chạy** rớt khỏi danh sách → player
  không bao giờ thấy nó xong → **hình kẹt ở 360p cho tới khi reload trang**, và thanh
  tiến độ cũng không hiện. Sửa hai chỗ: (a) `List` sắp việc **chưa xong lên trước** rồi
  mới tới recency; (b) **`useStream` tự poll 5 giây/lần khi chưa có `local`** — endpoint
  đó mới là nơi biết "phát bằng gì", nên việc nâng tầng không được phụ thuộc vào hàng đợi.
- **`watch_ratio` từng bị thổi phồng ngay tại nguồn (sửa 2026-07-29)**: client tính
  `element.currentTime / element.duration`, mà stream remux fMP4 khai báo độ dài **đang lớn
  dần** — nên phân số tiến về 1.0 ngay từ giây đầu. Ca đo được: video 243s xem tới 0:41 bị
  ghi là **92% hoàn thành**. Vì ranking coi watch_ratio là bằng chứng "video đáng mở", một
  phép chia đó âm thầm nói với nó rằng mọi thứ bị bỏ dở sớm đều xuất sắc. Giờ mẫu số lấy từ
  độ dài catalog. **Dữ liệu cũ vẫn còn lệch** — `BuildProfile` lấy `max()` nên giá trị bị
  thổi không tự phai đi.
- **`--flat-playlist` thiếu rất nhiều field**: không có channel per-entry (dùng `playlist_uploader`),
  không có view count, không có ngày đăng, không có `thumbnail` (dùng mảng `thumbnails`).
  **Không được default ngày đăng = now** — nó hiện thành "1 minute ago" trên mọi card.
- **yt-dlp đặt cùng tên file cho phụ đề người làm và máy làm** → phải chạy 2 lượt mới phân biệt được.
- **`ffmpeg` nuốt stdin** trong vòng lặp bash → luôn dùng `-nostdin`.
- **pgx encode nil slice thành NULL** → vi phạm NOT NULL của cột mảng.

## 9. Câu còn để ngỏ
- TV nhà là hãng gì? (ảnh hưởng cách xử lý cert)
- ~~Có SSD ngoài không?~~ **Có — `/Volumes/Data2/Youtube`, 437 GiB (2026-07-28).** Xem mục 2.
