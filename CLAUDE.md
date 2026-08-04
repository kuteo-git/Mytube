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
| `remux` | mux trực tiếp 2 luồng adaptive → fMP4, 1080p | ⚠️ mở lại |

**Chặng 2 xong 2026-07-29 — player leo đủ 3 tầng:**
`instant` phát trong ~17ms → dựng `remux` 1080p ở thẻ ẩn, lên khi sẵn sàng → `local` khi tải xong.
Không transcode, không HLS.

**Cờ ffmpeg chống lệch tiếng/hình (2026-07-29).** Hai input được seek **riêng biệt**, nên
timestamp phải chuẩn hoá chứ không được tin: `-avoid_negative_ts make_zero` gộp chúng về
cùng gốc, `-muxdelay 0 -muxpreload 0` bỏ độ trễ muxer tự chèn giữa hai luồng.
Và `-frag_duration 1000000`: chỉ dùng `frag_keyframe` thì fragment dài **1.9–4.9 giây và
không đều** (đo thật), mà trình duyệt đọc file đang lớn dần phải đợi trọn một fragment mới
trình bày được — nên tiếng và hình tới theo từng khối lệch nhau. Cắt đều 1 giây thì fragment
còn ~0.77s. Sau khi sửa: video và audio cùng `start_time`, trước đó lệch 0.04s.

**Seek ở tầng remux = mở lại luồng.** fMP4 qua pipe không có index nên trình duyệt không seek
được; player gửi `?t=<giây>` và ffmpeg dùng `-ss` **trước `-i`** (HTTP range seek, không phải
decode-rồi-bỏ). Đo thật: mở từ đầu 4.4s, seek tới 120s mất **3.0s** — rẻ hơn vì URL đã cache.
Chỉ bắn khi **thả tay** khỏi thanh seek; kéo thì chỉ số chạy. Có nhãn "Seeking…" vì 3 giây
mà hình vẫn đứng yên thì trông như bị lơ.

**Toàn bộ vị trí là tuyệt đối.** Luồng remux mở tại mốc nào thì tự cho mình bắt đầu từ 0, nên
mọi thứ ngoài thẻ video làm việc với `offset + currentTime`.

**Menu Auto / 1080p / 360p** — chỉ hiện mục video thật sự phục vụ được. **Ghim là lệnh**:
chọn tay thì không tự leo cũng không tự tụt. Auto mà remux không kịp trong 20s thì bỏ, về
360p và **không thử lại cho video đó** — mạng vừa không kham nổi thì lần hai cũng vậy.

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
- **Hạn ngạch feed do người dùng chỉnh được (2026-08-04) — `/settings` → "Home feed".**
  Ba thanh trượt, tổng luôn = 100, chia **82%** trang; 18% còn lại là **xem dở 10% +
  xem lại 8%**, **cố định, không chỉnh được** (đó là trạng thái lịch sử xem, không phải
  nguồn nội dung mới — bắt chúng tranh chỗ với gợi ý là sai bản chất).

  | Thanh | Nghĩa | Mặc định |
  |---|---|---|
  | Channels you follow | `profile.Subscribed[channel]` | 25% |
  | More of what you watch | chưa subscribe, `combinedAffinity ≥ 0.15` | 60% |
  | Something new | chưa subscribe, dưới ngưỡng đó | 15% |

  - **`feedSlot` tách khỏi `Reason`.** Reason trả lời *"vì sao video này ở đây"* — có 9 cái
    và **chồng lấn** (vừa chưa-xem vừa kênh-theo-dõi). Slot trả lời *"nó chiếm phần của ai"* —
    mỗi video đúng **một** slot. Gộp hai thứ này chính là lý do "hợp gu" trước đây không
    chỉnh được: nó nằm rải trong `NeverWatched` + `RecentlyAdded`, lẫn với mọi thứ mới khác.
    `dto.Reason` gửi cho client **không đổi**.
  - **Mặc định = đúng hạn ngạch cũ, quy đổi.** `NeverWatched 30 + RecentlyAdded 15` → nhóm
    hợp-gu; chuẩn hoá trên 82% ra 25/60/15. **Cài xong mà không kéo gì thì feed y hệt** —
    nếu mặc định đổi thì không phân biệt được "thanh trượt chạy" với "mặc định mới".
  - **0 là biến mất thật.** Bỏ sàn `take < 1` cho ba rổ này (giữ cho hai rổ cố định) —
    thanh kéo hết cỡ mà vẫn ra nội dung là nút nói dối. Video bị nén xuống **cuối** danh
    sách chứ không bị xoá: hạn ngạch chưa bao giờ bỏ video nào.
  - **Lưu ở gateway** (`config/feed-mix.json`, khuôn mẫu `translate_config.go`), gửi kèm
    trong `GetFeedRequest.mix`. **Recsys không giữ cấu hình nào** — xếp hạng vẫn là hàm
    thuần của request + signal. **Chung cho cả nhà**, không theo user: 2 tài khoản seed,
    chưa có màn đăng ký, nên "riêng từng người" là bảng + migration + RPC cho một sự riêng
    tư chưa ai yêu cầu. Đổi thì chỉ đổi chỗ đọc.
  - **Tỉ lệ, không phải số tuyệt đối**: 3/2/1 và 50/33/17 ra cùng một feed. UI ép tổng 100,
    server chuẩn hoá lại — nơi nào cũng đúng.
  - **Lưu xong huỷ cache `['feed']`** ở client. Feed đông cứng vào snapshot 30 phút, nên
    không huỷ thì thay đổi vô hình suốt nửa tiếng — không phân biệt được với hỏng.
    Đánh đổi: mất vị trí cuộn.
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
  | Affinity từ **dislike** | cùng ba trục, **phai** half-life 90 ngày | **×−0.7** |
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
  **Mở rộng 2026-08-04 — dislike giờ cũng dạy được, nhưng chỉ một phần ba:**
  - **Video bị bấm thì mất hẳn, mãi mãi.** Đó là quyết định, không phải sở thích;
    không phai, không có đường quay lại trong feed. Ba mục dưới đây chỉ nói về
    thứ dislike *dạy* cho hệ thống ngoài video đó.
  - **`buildDislikeAffinity` đối xứng với like** — cùng ba trục topic/kênh/hashtag,
    cùng trọng số trục — nhưng nhân **−0.7** so với **+2.0** của like. Lý do bất
    đối xứng: like thường khen *loại nội dung*, dislike thường nhắm *video này*
    (thumbnail, tiêu đề, hai giây đầu). Nghiêng feed, không xoá chủ đề.
    Trước đó dislike **không dạy gì cả**: từ chối 10 video cùng loại thì mất đúng
    10 video, cái thứ 11 đứng nguyên chỗ cũ.
  - **Phai half-life 90 ngày.** Mọi tín hiệu khác trong ranker đều phai; riêng cái
    này không, vì nó chỉ là `map[string]bool`. Giờ `UserProfile.Disliked` mang
    `occurred_at` (query vốn đã `ORDER BY occurred_at DESC`, chỉ là không lấy ra).
    Dislike không có ngày (dữ liệu cũ) tính **đủ 1.0**, không phải 0.
  - **Chặn cả kênh cần tỉ lệ, không chỉ số đếm**: `≥3` video **và** `≥30%` số video
    của kênh đó. Trước là đếm trần: 3/5 và 3/200 bị xử như nhau — cái đầu là phán
    quyết, cái sau là ba lần từ chối bình thường trong một thư viện dùng nhiều tháng.
    Kênh đã subscribe vẫn miễn nhiễm.
  - `penaltyDisliked = 5.0` **chưa bao giờ được dùng cho dislike** — nơi đọc duy
    nhất là `/2` cho video đã xem xong, mà video bị dislike thì `continue` từ mấy
    dòng trước. Đổi tên thành `penaltyAlreadyWatched = 2.5`, số không đổi.
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
3b. **Phát nền trên iOS là bất khả thi từ web (xác định 2026-08-02).** iOS treo cả
   `<video>` lẫn `AudioContext` khi tab vào nền hoặc khoá màn hình. Không có cờ nào,
   không có PWA nào vượt qua được. Đã làm những gì web cho phép:
   **Media Session** (metadata + nút trên màn khoá) và **Picture-in-Picture**.

   | | Android Chrome | iOS Safari |
   |---|---|---|
   | chuyển sang app khác | ✓ | ✓ **chỉ khi đang PiP** |
   | khoá màn hình | ✓ | ✗ |
   | điều khiển màn khoá | ✓ | ✗ |
   | narration (TTS) ở nền | ✗ | ✗ |

   **Narration ở nền — sửa một nửa 2026-08-03, và hai nửa là hai nguyên nhân khác
   nhau.** Ghi chú cũ ở đây gộp chúng làm một, sai:

   | Nguyên nhân | Ở đâu | Sửa được? |
   |---|---|---|
   | **timer bị bóp** → không đặt thêm lịch | Android, và tab ẩn trên desktop | **Đã sửa** |
   | **OS treo `AudioContext`** | iOS nền/khoá máy | Không, từ web |

   Cái thứ nhất mới là thứ người dùng gặp trên Android, và nó **không** phải giới hạn
   của trình duyệt: `source.start(when)` do **luồng audio** thực thi, không cần JS
   chạy. Nhưng tầm đặt lịch chỉ có 10 giây, nên vào nền là tick chết → im sau ≤10s.
   Giờ `PREFETCH_SEC = 60`.

   **Kèm theo bắt buộc**: dừng/tua chuyển sang **nghe sự kiện** của thẻ video
   (`bindNarration`) chứ không hỏi thăm mỗi 100ms. Đặt lịch xa mà vẫn dựa vào timer
   thì bấm dừng từ màn khoá — nơi timer không chạy — sẽ nghe thuyết minh nói tiếp cả
   phút sau khi hình đã đứng.

   **Đã thử `createMediaStreamDestination` → `<audio>` và ĐÃ BỎ (2026-08-03).**
   Giả thuyết: điện thoại giữ media element sống ở nền nên định tuyến qua đó có thể
   cứu iOS. Kết quả đo trên iPhone thật: **tệ hơn hẳn** — Safari đẩy MediaStream qua
   đường xử lý real-time communication, tiếng TTS bị **rè** và **ngắt sau ~1 giây**
   mỗi cue. Đã trả về `ctx.destination`. Đừng thử lại đường này.

   **Tiếng rè còn một nguyên nhân thứ hai, có sẵn từ trước:** `NARRATION_GAIN = 2.5`
   nhân với `video.volume`. Ở âm lượng tối đa thì mọi mẫu trên 0.4 biên độ bị cắt
   ngọn, mà TTS vốn được chuẩn hoá gần mức đầy — nên nó rè cả ở foreground, chỉ là
   không ai để ý cho tới khi có cái để so sánh. Giờ có `DynamicsCompressor` làm
   limiter trước `destination`: giữ độ to, bỏ phần cắt ngọn.

   Narration vẫn vỡ ở nền trên iOS. Cách duy nhất là server dựng sẵn một track rồi
   trộn vào file — và cái đó phải TTS toàn bộ video **trước khi nghe được câu đầu**,
   tức xoá mất tính chất "nghe được sau vài giây" vừa xây. Việc của một phase.

   Đây là lý do kỹ thuật cụ thể cho "app mobile" ở Phase 3 (§7): không phải để đẹp
   hơn, mà vì phát nền trên iPhone chỉ native mới làm được.
   **Bẫy đi kèm:** player có hai thẻ `<video>`; khi đổi tầng phải xin PiP lại trên
   thẻ mới **trước** khi xoá nguồn thẻ cũ, không thì cửa sổ PiP tắt giữa chừng.
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
Player: autoplay (bị trình duyệt chặn thì **để nguyên ở khung hình đầu**, không tự bật muted —
một video rõ ràng chưa chạy dễ hiểu hơn một video trông như đang chạy mà không có tiếng).
**Code đã từng trôi khỏi luật này và bị kéo về 2026-08-03**: có lúc nó tự bật muted rồi phát
tiếp, kèm một bộ "khôi phục tiếng ở cử chỉ đầu tiên". Trên desktop nghe như một sự tiện lợi;
trên iPhone thì Safari từ chối autoplay có tiếng gần như luôn luôn, nên nhánh dự phòng **chính
là** kết quả bình thường — video phát câm. Và gỡ câm không chỉ là gán `muted = false`: Safari
đòi gọi lại `play()` trong cùng cử chỉ, mà không chỗ nào làm. Đó là lý do luật này tồn tại.

Điều khiển **tự ẩn sau 3 giây** với chuột, **5 giây với ngón tay** (hiện lại khi rê/bấm/focus,
luôn hiện khi pause hoặc đang mở menu). **Chuột: click = play/pause. Cảm ứng: chạm = bật/tắt
menu** — ngón tay không rê được nên chạm là cách duy nhất gọi menu lên, mà nếu nó cũng
play/pause thì mỗi lần nhìn điều khiển đều cắt ngang cái đang xem. Phân biệt bằng `pointerType`
của chính sự kiện, không đoán theo bề rộng màn hình.
**`pointerleave` KHÔNG được dùng để ẩn trên cảm ứng**: con trỏ chạm không rời đi mà *ngừng tồn
tại*, trình duyệt báo bằng cùng sự kiện — nên mọi cú chạm tự ẩn menu lúc nhấc tay, và vì chuỗi
là `pointerdown → pointerup → pointerleave → click`, thanh bị tắt `pointer-events` đúng một sự
kiện trước cú click dành cho nó. Đó là lý do "bấm menu không ăn" trên iPhone.
Trên cảm ứng, thanh gọn lại: bỏ slider âm lượng (có nút cứng), còn phụ đề/chất lượng/thuyết
minh/tự động phát gom vào nút ⚙; vùng chạm 44px thay vì 36.
**Bảng ⚙ trên cảm ứng phải portal ra `document.body`**, không được là dropdown trong player:
player có `overflow-hidden` (cần, để giữ bo góc) và trên điện thoại chỉ cao ~220px, nên một
danh sách mở lên trên bị **cắt mất phần đầu**. Đã gặp thật.
**Cử chỉ vuốt-xuống-thu-nhỏ đã BỎ (2026-08-03)** — người dùng thấy vô dụng. Gỡ cả bộ máy chứ
không chỉ ngắt dây. Hệ quả: trên điện thoại, trang watch **không còn** trạng thái mini nào,
vì `IntersectionObserver` vốn chỉ chạy ở desktop. Bar chỉ xuất hiện khi rời trang watch.
**Mute không được ghi từ `volumechange`.** Sự kiện đó bắn cho cả thay đổi của chính player
(ducking cho thuyết minh, và trước đây là autoplay policy tự tắt tiếng). Nó từng ghi vào
`localStorage`, nên một lần bị chặn autoplay là im lặng được lưu như **sở thích** và trả lại ở
mọi lần mở sau — không cách nào phân biệt với lựa chọn thật. Đã đổi khoá sang
`yt-player-muted-v2` để bỏ các giá trị cũ, và chỉ ghi từ nút tắt tiếng và thanh âm lượng.
**Toàn cảnh và PiP: có `webkit*` thì ƯU TIÊN `webkit*`, không phải ngược lại (sửa 2026-08-03).**
Lần đầu tôi cho API chuẩn đi trước và chỉ rơi xuống webkit khi chuẩn vắng mặt — sai. Safari
trên iPhone **có** báo hỗ trợ Fullscreen API, nhưng thứ nó cho là phần tử phóng to *trong
trang*: **không xoay ngang**, và không có trình phát hệ thống để bàn giao trạng thái phát lúc
thoát. `webkitEnterFullscreen` mới mở trình phát gốc của Apple. Cùng lý do cho
`webkitSetPresentationMode`.
Khả năng "trình duyệt có biết khái niệm này không" hỏi từ `HTMLVideoElement.prototype` (ref
rỗng ở render đầu). Nhưng "video NÀY có đủ điều kiện không" thì chỉ phần tử trả lời được —
`webkitSupportsPresentationMode`, hỏi trong effect sau khi phần tử tồn tại.
**Thoát toàn cảnh trên iOS trả video về ở trạng thái dừng** dù lúc vào đang chạy. Nghe
`webkitbeginfullscreen`/`webkitendfullscreen` để nhớ và trả lại — không thì rời một video, quay
về một khung hình đứng. **Hai cái bẫy, bản sửa đầu dính cả hai:**
(a) trí nhớ "lúc vào đang chạy" **không được** để trong closure của effect — nguồn có thể đổi
giữa chừng (file local tải xong) làm effect chạy lại và mang trí nhớ đi mất; phải là ref.
(b) `pause` **không chắc** đến trước `webkitendfullscreen`; kiểm một lần tại thời điểm đó có
thể thấy video chưa dừng, rồi nó dừng ngay sau. Phải bắt cả cú `pause` đến muộn.
**Mở lại app thì video dở dang được đưa lại vào góc, ĐANG DỪNG (2026-08-03).**
`last-watched.ts` giữ `{videoId, position, savedAt}` trong localStorage, ghi cùng nhịp với
`recordProgress`. Ba luật: xem quá **95%** thì coi như xong và **quên**; quá **7 ngày** thì
không còn là "vừa nãy" nữa; bấm ✕ là câu trả lời cho lời mời, nên cũng quên.
**Không tự phát** — tiếng phát ra từ góc màn hình của một trang vừa mở là giật mình, và đây sẽ
là lần thứ hai trong dự án video tự chạy khi không ai bảo.
**Không dùng history của server** cho việc này: history nói *đã từng xem gì, trên mọi máy*, còn
đây nói *trình duyệt NÀY đang xem dở gì*. Dùng history sẽ mời lại thứ đã xem xong ở máy khác.
Vị trí thì ưu tiên số của server, chỉ dùng số local khi tab đóng trước lần báo cuối.

**Padding thống nhất: `px-4` trên điện thoại, `px-6` từ 700px.** Áp cho mọi trang.
Riêng `ChipBar` ở Home tràn ra mép trên điện thoại (`-mx-4` + `px-4` bên trong): một dãy cuộn
ngang mà dừng trước mép trông như đã hết chứ không như còn tiếp.
**Bẫy đã gặp:** WatchPage từng chừa `pt-[calc(3.5rem+56.25vw)]` — cộng **thừa** chiều cao
`TopBar`, vì `sticky` **vẫn chiếm chỗ trong luồng** nên nội dung đã bắt đầu ở dưới nó rồi. Thừa
đúng 56px giữa hình và tiêu đề. Giờ chỉ còn `pt-[56.25vw]`.

`Space`/`←`/`→`/`m`, volume slider, buffered range thật, phụ đề (en/vi) với menu CC — phụ đề
được tải **trước** file video nên xem được ngay trong lúc còn phát upstream. Hết video thì
đếm ngược 5 giây rồi phát video kế (có công tắc Autoplay, tự dừng sau 3 video không ai tương tác).

**Search:** **luôn** hỏi YouTube song song với thư viện. Trang kết quả 2 khối
(In your library / On YouTube). Autocomplete từ local (chủ đề → kênh → tiêu đề).
Bỏ dấu tiếng Việt hai chiều qua `unaccent`. Mở video từ YouTube → ghi metadata
**không gán chủ đề**, feed vẫn do topics.yaml quyết định.

**Phân trang:** feed + search dùng `useInfiniteQuery`, tự nạp trước 600px, kèm nút
"Load more" thật cho bàn phím/remote.

**Activity:** trang `/activity` gộp hàng đợi tải (kèm lỗi yt-dlp nguyên văn) và **lịch sử
quét** (kèm nguồn nào hỏng). Có nút "Scan now" thật. Không phải log viewer —
log 4 service vẫn ra stdout.

**Mở rộng 2026-08-04:**
- **Lịch sử quét, không phải một lượt.** Trước đây `Scanner.lastScan` là **một biến trong
  RAM**: chỉ trả lời được "lượt gần nhất thế nào", và restart là mất luôn cả câu đó. Câu
  người dùng thật sự hỏi trải dài nhiều ngày ("kênh này im mấy hôm rồi, quét có chạy
  không"). Giờ có bảng `ingest.scans`, ghi sau mỗi lượt, **tự xoá dòng cũ hơn 30 ngày ngay
  trong lần ghi** — buộc cái làm bảng phình cũng là cái làm nó co, không cần lịch thứ hai.
  Dọn **theo tuổi chứ không theo số dòng**: 500 dòng là 3 tuần ở nhịp 1 tiếng nhưng 10 ngày
  ở `SCAN_INTERVAL=30m` — ý nghĩa đổi mà không ai đụng vào. `GET /api/scans?limit=&offset=`,
  **phân trang thật ở server** vì bảng này tăng 1 dòng/giờ, mãi mãi.
- **"View more" 10 một lần.** Jobs phân trang **ở client** (`usePagedList`): ba nhóm
  Failed/In progress/Completed đến từ **cùng một request**, nên phân trang server sẽ thành
  ba query ba con trỏ cho một trang chẩn đoán. Scans thì ngược lại — phân trang server.
- **`[X]` trên mọi dòng job, hai nghĩa theo trạng thái**: đang chạy = **huỷ tải**; đã xong
  hoặc đã lỗi = **ẩn** (`jobs.dismissed_at`, `POST /api/ingest/jobs/{id}/dismiss`). Cùng vị
  trí cùng icon, phân biệt bằng `aria-label`/tooltip — nút mà đổi chỗ là nút phải đi tìm.
  Store chỉ cho ẩn **trạng thái kết thúc**: giấu việc đang chạy là thứ duy nhất nút này
  không được phép làm.
- **`hideDismissed` là tham số, không phải luật.** Hai bên đọc cùng `GET /api/ingest/jobs`
  vì hai lý do khác nhau: trang Activity đang dọn dẹp, còn **player đọc để biết bản tải đã
  về chưa**. Lọc mặc định thì ẩn một job xong có thể để player chờ mãi một bản đã có — đúng
  hình dạng sự cố "job rớt khỏi danh sách → kẹt 360p" ở trên, từ hướng khác. Cùng lý do,
  query key của React Query **tách riêng** (`['ingest-jobs','activity',limit]`): chung khoá
  thì trang Activity nạp 200 sẽ đè cache 50 của player.
- **Retry trên mỗi dòng Failed** (`POST .../retry`): tạo **job mới** với cùng `sourceUrl` và
  cùng `preferredHeight`, rồi ẩn dòng cũ. Không reset dòng cũ tại chỗ — `attempts`, thời
  điểm và thông báo lỗi **chính là** thứ trang này tồn tại để hiện. Từ chối job chưa kết
  thúc: hai transfer cùng một video là đúng thứ đã làm địa chỉ này bị chặn (§8 rủi ro 5).
  Có Retry vì **không có đường hoàn tác cho `[X]`** (đã chốt): nếu thứ duy nhất làm được với
  một lỗi là giấu nó thì giấu sẽ thành cách xử lý lỗi mặc định.
- **Lịch sử quét không có `[X]`** — nó không đòi hành động nào, và ẩn một dòng tạo ra một lỗ
  trong thứ vốn để đọc như chuỗi liên tục: "tuần trước có chạy không" sẽ trả lời sai.

**Feed vô tận:** `GetFeedPage` đông cứng thứ tự rank vào một snapshot theo phiên (memory
recsys, TTL 30 phút) — trang sau đọc từ snapshot đó thay vì rank lại, nên không trùng video.
Khi snapshot còn dưới 48 video, gateway tự gọi `ExpandLibrary` (ingest) chạy nền: đào sâu
source trong topics.yaml (có cursor lưu Postgres để lần sau tiếp tục từ chỗ cũ) → related qua
InnerTube (`/youtubei/v1/next`, tự viết, không có contract) → search theo tên chủ đề. Chỉ một
lượt expand chạy cùng lúc.

**Huỷ tải khi rời video (2026-07-29).** Bấm play xếp hàng một bản tải để lần sau xem từ đĩa —
nhưng bản tải mà **không còn ai chờ** thì cũng là request tới YouTube không còn ai chờ, và
địa chỉ này đã bị chặn một lần vì làm quá nhiều (§8 rủi ro 5). Rời trang watch →
`POST /api/videos/{id}/download/cancel`. Gắn ở **cleanup của effect** nên phủ cả hai chiều
rời đi: chuyển sang video kế, và đóng trang.
**Đánh đổi đã biết:** `NoPart()` đang bật nên yt-dlp **không resume** — huỷ nửa chừng là mất
sạch phần đã tải, lần sau bắt đầu lại từ 0.

**Eviction:** catalog chạy sweep mỗi giờ (`services/catalog/internal/usecase/evict.go`).
Vượt 20 GiB → xoá file media của LRU không pinned về 16 GiB, giữ metadata + thumbnail +
history. Ngưỡng chỉnh qua `EVICTION_HIGH_BYTES`/`EVICTION_LOW_BYTES`.

### Chưa làm — thứ tự đề xuất khi làm tiếp

(Nội dung bên dưới đã lỗi thời sau session 2026-07-31; xem mục mới bên dưới.)

1. ~~**3 trang còn thiếu**: `/history` · `/saved` · `/storage`. API đã có sẵn
   (`ListHistory`, `GetStorageUsage`, `SetPinned`) — chỉ thiếu tầng `ui/`.
   **Đang là link chết trong sidebar.**~~ **ĐÃ LÀM 2026-07-31.**

### Đã làm 2026-07-31 — session "nine bugs" follow-up

#### 3 trang mới
| Trang | Thành phần |
|---|---|
| `/history` | `HistoryPage.tsx` — infinite scroll `GET /api/history`, grid `VideoCard` |
| `/saved` | `SavedPage.tsx` — infinite scroll `GET /api/pinned` (pinned videos), grid `VideoCard` |
| `/storage` | `StoragePage.tsx` — stats cards (`usedBytes`/`budgetBytes`/etc.) + eviction candidates grid with inline Pin/Unpin |

#### Sidebar
- Thêm 3 link: `Bookmark` → `/saved`, `Clock` → `/history`, `HardDrive` → `/storage`

#### Backend mới
- `POST /api/videos/{id}/pinned` — REST route cho `SetPinned` RPC (trước đó RPC có nhưng chưa expose)
- `ListPinnedVideos` RPC — thêm proto → domain → postgres repo → use case → RPC server → gateway `GET /api/pinned`

#### Keep/Pin UI
- `VideoActions.tsx`: nút "Keep"/"Kept" đã có onClick gọi `useSetPinned`, bookmark fill khi pinned
- `VideoCard.tsx`: nút ⋮ mở dropdown menu "Keep"/"Unkeep"
- `StorageBanner.tsx`: nút "Manage storage" dead link → `<Link to="/storage">`

#### Cải thiện feed ranking (`ranker.go`)
- **Lọc FAILED**: `f.MediaState == "MEDIA_STATE_FAILED"` → skip
- **Lọc EVICTED**: `f.MediaState == "MEDIA_STATE_EVICTED"` → skip
- **Lọc 85%+ watched**: `fraction >= 0.85` → skip (giữ trong up-next, chỉ lọc ở Home)
- **Penalty publishedAt**: `score *= exp(-days/365)` với 365-day half-life; nếu >1yr thì trừ thêm -4.0 flat. **Đã lỗi thời: giờ hard filter skip toàn bộ video >1yr, dùng AddedAt fallback khi không có PublishedAt (2026-07-31).** Xem session "old videos" follow-up.

#### Cải thiện "Popular with you" (`collections.go`)
- **Chỉ READY**: lọc `dto.MediaState != "READY"`
- **Composite hot score**: `viewCount × recencyMultiplier(addedAt, <30d) × log2(duration+1) × exp(-pubDays/365)` — thay vì chỉ sort theo view_count
- **Recency decay**: addedAt 0→30 ngày: 1.0→0.3; publishedAt dùng exponential decay 365-day half-life

#### YouTube topic injection (`HomePage.tsx`)
- Khi browse topic (không phải "All"), gọi `useDiscover(topicName, 6)` → hiện row "From YouTube · {topic}" dùng `ExternalVideoCard`

#### Superpowers plugin
- Thêm `"plugin": ["superpowers@git+https://github.com/obra/superpowers.git"]` vào `~/.config/opencode/opencode.json` (global)

### Đã làm 2026-07-31 — session "old videos" follow-up

#### Hard filter homepage videos >1yr (`ranker.go`)
- **`maxPublishedAgeDays = 365`** (constant at `ranker.go:132`): hard filter trong `rankAll` — skip video published >365 ngày trước. Penalty cũ (multiplicative + -4.0 flat) không đủ vì `applyDiscoveryQuota` xếp lại theo reason bucket bất kể absolute score.
- **Epoch detection**: `hasPub = !PublishedAt.IsZero() && PublishedAt.Unix() > 0` — protobuf decode nil `Timestamp` thành `1970-01-01T00:00:00Z`, **KHÔNG** phải Go zero time (`0001-01-01`). `IsZero()` trả false cho epoch, thành ra `20665 days > 365` filter hết toàn bộ video (3567/3722). Dùng `Unix() > 0` để bắt cả hai trường hợp.
- **AddedAt fallback**: video không có `PublishedAt` (824 video, do flat-listing scan không trả) thì dùng `AddedAt` làm proxy.

#### Backfill mở rộng: điền `published_at` cho video thiếu
- **`server.go:288` bug**: `ListVideoFeatures` RPC không populate `PublishedAt` vào proto response → ingest client không thấy field này. Fix: thêm `if !f.PublishedAt.IsZero() { feat.PublishedAt = timestamppb.New(f.PublishedAt) }`.
- **Rename `ListVideosMissingTopics` → `ListVideosNeedingBackfill`**: thay vì chỉ chọn video thiếu topic, giờ chọn video thiếu topic **hoặc** thiếu `published_at` (có topic từ `topics.yaml` nhưng chưa có date).
- **`VideoRef.MissingPublishedAt`**: flag để `backfillOne` biết video này cần date chứ không cần topic → bỏ qua `preview.Category == ""` check, luôn upsert để ghi date.
- **Không đổi proto hay endpoint**: `POST /api/topics/backfill` vẫn hoạt động như cũ, chỉ rộng hơn.
- **824 video thiếu date** cần ~55 phút chạy backfill (1 luồng, 4s giữa các call).

#### Bugfix ingest service stale binary
- Binary `/tmp/local-youtube/ingest` compile trước commit port-change (17:54 vs 18:03 Jul 28), default catalog URL = `:8081` thay vì `:8181` → "connection refused". Rebuild + restart.

#### Bugfix gateway missing MEDIA_ROOT
- Gateway chạy không có env `MEDIA_ROOT`, default về `./media` thay vì `/Volumes/Data2/Youtube` → `/media/...` trả 404. Restart với `MEDIA_ROOT=/Volumes/Data2/Youtube`.

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
- **Đường TẢI XUỐNG không lọc codec, chỉ đường remux có (sửa 2026-07-29)**: `Download`
  dùng `bestvideo[height<=N]+bestaudio` trần, nên yt-dlp lấy "tốt nhất" = **AV1**.
  Đo thật trên đĩa: **28 file AV1 + 4 AV1 + 2 VP9, không một file h264 nào**. Trái thẳng
  quyết định "ưu tiên h264 vì TV cũ" ở mục trên — và trớ trêu là **bản local (thứ thay thế
  stream) mới là bản có nguy cơ không phát được**, trong khi stream remux thì h264.
  Giờ `downloadFormat` sao chép đúng `remuxFormat`. Kiểm trên `rYap5zVNYf8`:
  cũ → `av01` itag 399; mới → `avc1` itag 299, **vẫn 1080p**.
  **File đã tải trước đây vẫn là AV1** — phải xoá và tải lại mới đổi được.
- **`watch_ratio` từng bị thổi phồng ngay tại nguồn (sửa 2026-07-29)**: client tính
  `element.currentTime / element.duration`, mà stream remux fMP4 khai báo độ dài **đang lớn
  dần** — nên phân số tiến về 1.0 ngay từ giây đầu. Ca đo được: video 243s xem tới 0:41 bị
  ghi là **92% hoàn thành**. Vì ranking coi watch_ratio là bằng chứng "video đáng mở", một
  phép chia đó âm thầm nói với nó rằng mọi thứ bị bỏ dở sớm đều xuất sắc. Giờ mẫu số lấy từ
  độ dài catalog. **Dữ liệu cũ vẫn còn lệch** — `BuildProfile` lấy `max()` nên giá trị bị
  thổi không tự phai đi.
- **Trang kênh từng dùng handle thay vì id (sửa 2026-07-29)**: gateway đổi `UC…` id thành
  `@handle` trước khi gọi `ListChannelUploads`, với lý do ghi trong comment là "YouTube giải
  handle đáng tin hơn". **Ngược lại**: `UC…` id **chính là** `browseId` của InnerTube, không
  cần giải; handle cần thêm một bước và bước đó hỏng thật (`@tinhte`, `@guinnessworldrecords`).
  Hỏng → rơi xuống flat listing → **cả trang kênh không có ngày đăng và view = 0**, trong khi
  kênh khác đủ cả. Sau khi ưu tiên id: **0/30 → 30/30**.
  **Còn lại**: scanner vẫn dùng handle vì `topics.yaml` ghi nguồn dạng `youtube.com/@x/videos`,
  nên video quét về từ những kênh handle-hỏng vẫn thiếu ngày/view.
- **`--flat-playlist` thiếu rất nhiều field**: không có channel per-entry (dùng `playlist_uploader`),
  không có view count, không có ngày đăng, không có `thumbnail` (dùng mảng `thumbnails`).
  **Không được default ngày đăng = now** — nó hiện thành "1 minute ago" trên mọi card.
- **yt-dlp đặt cùng tên file cho phụ đề người làm và máy làm** → phải chạy 2 lượt mới phân biệt được.
  **Sửa 2026-08-02 — vẫn 2 lượt, nhưng song song.** Trước đây hai lượt chạy tuần tự và phân
  biệt authored/auto bằng **thứ tự**: cái gì có mặt sau lượt một là do người làm. Nghĩa là lượt
  hai phải đợi lượt một chỉ vì một va chạm tên file. Giờ mỗi lượt ghi vào **thư mục tạm riêng**
  (`.subs-authored` / `.subs-auto`), chạy đồng thời, rồi hợp nhất — cùng câu hỏi nhưng trả lời
  bằng **chỗ ghi** thay vì thứ tự, và không ai phải đợi ai.
  **Và chỗ gọi đã đổi**: `FetchSubtitles` trước chỉ chạy trong worker, tức phải xếp hàng sau
  `pollInterval` (3s) rồi sau `Preview` (~2s) rồi mới tới hai lượt tuần tự — đo được **5–12
  giây**, rơi đúng vào cửa sổ phụ đề cần nhất (lúc đang xem bản upstream chất lượng thấp).
  Giờ `Submit` bắn nó ngay khi **bấm play**, chạy nền. `FetchSubtitles` tự bỏ qua nếu file đã
  có trên đĩa, nên worker và đường mới không làm hai lần.
  **Không bao giờ chạy khi `?prefetch=1`** — rê chuột qua feed là hàng chục card, mỗi card một
  full extract cho video chưa ai chọn xem. Đó đúng hình dạng sự cố §8 rủi ro 5. Chỉ bấm play
  mới tới được `Submit`, nên ranh giới nằm sẵn ở đó chứ không phải một cái `if` phải nhớ.
- **`ffmpeg` nuốt stdin** trong vòng lặp bash → luôn dùng `-nostdin`.
- **pgx encode nil slice thành NULL** → vi phạm NOT NULL của cột mảng.

## 9. Câu còn để ngỏ
- TV nhà là hãng gì? (ảnh hưởng cách xử lý cert)
- ~~Có SSD ngoài không?~~ **Có — `/Volumes/Data2/Youtube`, 437 GiB (2026-07-28).** Xem mục 2.
