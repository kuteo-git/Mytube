# Cast sang loa (R1 + Google Nest) từ myTube — Plan triển khai + QA

> Ngày: 2026-10-31. Plan này để giao cho AI/dev khác làm. Mọi quyết định bên dưới **đã chốt với
> chủ nhà**, đừng đổi lại; gặp mâu thuẫn với code thật thì **dừng lại hỏi**, đừng tự đoán.
>
> Ba repo liên quan:
> - **Youtube** (`/Volumes/Data2/git/Youtube`): server myTube (Go microservices + web React). Đọc
>   `CLAUDE.md` của repo này trước tiên, nhất là §3 Architecture, §4b Code conventions, §5 Frontend,
>   mục Logs và Build status.
> - **mytube-app** (`/Volumes/Data2/mytube-app`): app native Kotlin Multiplatform (Android + iOS).
>   Đọc `CLAUDE.md` và `docs/changelog.md` của repo này.
> - **robot-esp32** (`/Volumes/Data2/git/robot-esp32`): xiaozhi-server (Python), nơi phát nhạc cho
>   loa R1. File chính là
>   `xiaozhi-esp32-server/main/xiaozhi-server/plugins_func/functions/play_youtube.py`.
> - **xiaozhi-android** (`/Volumes/Data2/git/xiaozhi-android`): app chạy trên loa R1. Dự kiến
>   **không phải sửa** (xem §2.3).

---

## 0. Tóm tắt các quyết định đã chốt

| Chủ đề | Quyết định |
|---|---|
| Thiết bị đích | Loa R1 + Google Nest Audio + 2 Nest Mini. Nhóm loa tạo trong Google Home sẽ tự hiện. **Không** có Chromecast TV (`cast_type == "cast"`). |
| Số thiết bị mỗi phiên | 1. Không phát nhiều phòng song song. |
| Client có nút cast | Web, app Android, app iOS. **Không** có Android TV. |
| Trung tâm điều khiển | Client **chỉ** gọi gateway myTube (REST). Client không nói chuyện trực tiếp với R1 hay loa Google, và không dùng Cast SDK. |
| Tìm R1 | Đọc từ file cấu hình `data/cast-devices.json`, kèm kiểm tra R1 còn sống (timeout khoảng 1s). |
| Tìm loa Google | Sidecar Python dùng **pychromecast** tự tìm qua mDNS `_googlecast._tcp`. |
| Nguồn âm thanh | File trên myTube. Nếu video đã có trên đĩa thì tách audio từ mp4 (codec AAC đã kiểm tra, copy thẳng không encode lại). Nếu chưa tải thì phát ngay từ YouTube qua đường adaptive audio của ingest. |
| Hàng đợi | **Phía server myTube giữ hàng đợi.** Loa chỉ nhận từng bài một, phát xong mới được gửi bài kế. Hết hàng thì dừng, **không** tự thêm bài liên quan. |
| Phiên cast | Lưu trong **Postgres**, nên restart không mất phiên. Mọi máy trong nhà đều thấy và điều khiển được. |
| Tranh loa | Ai bấm sau thì thắng. Remote báo "Loa đã chuyển sang nguồn khác" rồi đóng phiên, không tự giành lại loa. |
| Trải nghiệm khi đang cast | Video trên máy dừng, màn hình chuyển thành remote. |
| Remote có gì | Ảnh bìa, tên bài, thanh seek, play/pause, next, stop, âm lượng, sửa hàng đợi (xoá, đổi thứ tự, nhảy tới bài), nút "Ngắt cast" để phát tiếp trên máy từ đúng chỗ đang nghe. |
| Lịch sử xem | Ghi tiến độ cho profile đã cast, khoảng 10 giây một lần và khi hết bài. |
| Livestream | Ẩn nút cast. |
| Cập nhật trạng thái | Client poll 1s khi remote đang mở. |
| Thứ tự làm | **P1**: phần lõi + R1 + web. **P2**: loa Google. **P3**: remote trong app Android/iOS. |

### Một điều chỉnh do kiến trúc myTube (giữ nguyên ý định của quyết định)
Theo `CLAUDE.md` của myTube, gateway **không có DB**. Mỗi service có schema và role riêng, và
*"No service queries another service's database"*. Vì vậy, phần "server giữ phiên + Postgres" sẽ
là một **Go service mới tên `cast`** (schema `cast`). Với client, gateway vẫn là cửa duy nhất: gateway
chỉ chuyển REST sang ConnectRPC.

---

## 1. Kiến trúc

```
 Web / Android / iOS
        │  REST/JSON (poll 1s khi remote mở)
        ▼
 gateway :8180 ──────── GET /api/videos/{id}/audio  (audio cho loa tải về)
        │ ConnectRPC                    │ có file trên đĩa → <mediaRoot>/<id>/audio.m4a (ffmpeg copy, cache lại)
        ▼                               │ chưa có file    → proxy sang ingest /audio/{id} (adaptive m4a)
 cast service :8186  (Go, MỚI)
   ├─ domain: Session, Queue, Reconcile() (hàm thuần)
   ├─ engine: mỗi phiên đang chạy có 1 vòng lặp 1s: Status → Reconcile → Actions
   ├─ Postgres schema `cast`
   ├─ catalog.RecordWatchProgress (ConnectRPC)
   └─ drivers:
        ├─ R1Driver ──HTTP──► R1 :8088 /api/media/*  ──WS──► xiaozhi-server play_youtube.py
        │                                                    (tải audio_url → mp3 → stream xuống R1)
        └─ GoogleDriver ─HTTP─► castbridge :8187 (Python, pychromecast, MỚI) ──Cast──► Nest
```

Port: 8185 đã được transcript dùng (đã kiểm tra). `cast` dùng **8186**, `castbridge` dùng **8187**.
Trước khi code, kiểm tra lại hai port này bằng `lsof -nP -iTCP:8186 -sTCP:LISTEN`.

Khởi động: thêm cả hai service vào `scripts/dev.sh` (không sửa `boot.sh`: nó chỉ chờ volume và Postgres rồi chạy `serve.sh` → `dev.sh`), giống cách đang chạy
`translate_server.py`. **Không tạo LaunchAgent mới**, vì `com.luke.local-mytube` đã chạy cả stack.
Thêm port mới vào vòng in trạng thái ở cuối `dev.sh` và vào danh sách port của `stop.sh`.

---

## 2. Dữ kiện từ code hiện tại (đã đọc, dùng làm hợp đồng)

### 2.1 API điều khiển của R1 (NanoHTTPD, `xiaozhi-android/.../control/ControlServer.kt`)
- `POST http://10.25.113.209:8088/api/media/play`, body `{"items":[...], "start_index":N}`.
  Mảng `items` được chuyển **nguyên JSON** qua `MediaCommands.Play(itemsJson)`, rồi
  `Protocol.sendMediaPlay` gửi qua WebSocket `{"type":"media","action":"play","items":[...]}`, rồi
  tới `core/handle/mediaHandle.py: handle_media_play` và `play_media_queue(conn, songs, start_index)`.
  ⇒ **Field mới thêm vào từng item sẽ tới được server mà không phải sửa app R1.** Field đặt ở cấp
  ngoài cùng (ngoài `items`) thì bị bỏ mất.
- `GET /api/media/pause`, `/resume`, `/next`, `/stop`, `/seek?position_s=N`.
- `GET /api/media/state` trả về:
  `{state: idle|downloading|playing|paused|stopped, video_id, title, artist, cover_url, duration_s, position_s, download_percent, queue[]}`.
  Khi đã dừng thì `video_id` là null.
- Âm lượng: `GET /api/state` có `volume` (%, đã làm tròn theo 15 nấc) và `volume_steps`.
  Đặt âm lượng bằng `GET /api/set?key=volume&value=<0..100>`.

### 2.2 xiaozhi-server `play_youtube.py`
- `_play_queue(conn, initial_queue, start_index, interrupt_current)` tải từng bài qua pytube
  (`_download(video_id, cache_dir)` → mp3), cắt bằng `_trim_mp3` khi có `song["_seek"]`, rồi đưa
  vào hàng phát qua `_queue_file`.
- **Đã có sẵn và gateway dùng được:**
  - `song["_seek"]`: bắt đầu phát từ giây thứ N.
  - `song["_silent"]`: không đọc câu "Đang tải bài …".
- Khi hàng còn dưới 2 bài, hàm `_related` tự thêm bài liên quan. **Phải tắt hành vi này cho bài
  đến từ myTube.**
- Khi tranh loa bằng giọng nói ("phát bài X"), server tạo session mới, nên `video_id` trong state
  của R1 đổi sang bài khác.
- Khi người dùng gọi wake word để hỏi giữa bài, nhạc dừng, R1 trả lời xong rồi phát tiếp
  (`_wait_qa_done`). Trong lúc đó state vẫn là `playing` và `position_s` **đứng yên**.

### 2.3 myTube
- Gateway: route khai trong `services/gateway/internal/api/router.go`. `g.userID(r)` cho biết profile.
  Đã có `/api/videos/{id}/stream`, `/instant`, `/remux` và `/hls/{name}`.
- `/remux` proxy sang ingest `GET /stream/{id}` (`services/ingest/internal/adapter/httpapi/remux.go`).
  Ingest dựng luồng này từ các track adaptive. **Endpoint audio cho video chưa tải phải dùng lại
  đúng cách resolve và retry 403 của remux.go.**
- Quy tắc trong charter: *"Never ask googlevideo for an open-ended range"*. Mọi request tới
  googlevideo phải có Range giới hạn.
- File trên đĩa: `<mediaRoot>/<video_id>/1080p.mp4`, audio là AAC (đã ffprobe 3/3 file).
- Ghi tiến độ: `catalog.RecordWatchProgress` (proto `RecordWatchProgressRequest`, có `user_id`,
  `video_id` …). Đọc proto để biết đủ các field.
- Mỗi service một schema và một role, tạo bằng vòng lặp trong `db/bootstrap.sql`. Migration nằm ở
  `services/<svc>/migrations/NNNN_*.sql`.
- Web chia theo feature: `src/features/<f>/{domain,application,infrastructure,ui}`. `ui/` **không
  bao giờ** gọi `fetch`.
  - Quy tắc "RENDER NO DEAD BUTTONS": nút không làm được việc thì phải ẩn đi.
  - i18n bằng key, có cả en và vi. Dịch thiếu là build fail.
- **Code, identifier, comment và commit message đều viết tiếng Anh.**

### 2.4 Mạng LAN (quét `dns-sd -B _googlecast._tcp` lúc 20:17 ngày 31/10)
Thấy `Nest-Audio-…`, 2 × `Google-Nest-Mini-…` và `Chromecast-…` (TV, loại bỏ).

---

## 3. Hợp đồng (contracts)

### 3.1 REST trên gateway (client gọi)
Tất cả nằm dưới `/api/cast`. Profile lấy từ `g.userID(r)`.

| Method & path | Body / Query | Trả về |
|---|---|---|
| `GET /api/cast/devices` | – | `[{id, name, kind:"r1"\|"google", model, online, active_session_id?}]`. Chỉ trả thiết bị `online`. |
| `GET /api/cast/sessions` | – | Các phiên đang chạy: `[SessionView]` |
| `POST /api/cast/sessions` | `{device_id, items:[{video_id}], start_index, start_position_s}` | `SessionView`. Thiết bị đang có phiên khác thì phiên cũ kết thúc với `ended_reason="replaced"`. |
| `GET /api/cast/sessions/{id}` | – | `SessionView` (kể cả phiên đã kết thúc, để remote hiện lý do) |
| `POST /api/cast/sessions/{id}/pause` · `/resume` · `/next` · `/stop` | – | `SessionView` |
| `POST /api/cast/sessions/{id}/seek` | `{position_s}` | `SessionView` |
| `POST /api/cast/sessions/{id}/volume` | `{percent}` (0..100) | `SessionView` |
| `POST /api/cast/sessions/{id}/jump` | `{index}` | `SessionView` |
| `PUT /api/cast/sessions/{id}/queue` | `{version, items:[{video_id}], current_index}` | `SessionView`, hoặc **409** nếu `version` đã cũ |
| `POST /api/cast/sessions/{id}/handoff` | – | `{video_id, position_s}`. Dừng loa, kết thúc phiên với `ended_reason="handoff"`. Client tự phát tiếp ở máy. |

```jsonc
// SessionView
{
  "id": "uuid", "device": {"id": "...", "name": "...", "kind": "r1"},
  "state": "loading|playing|paused|ended",
  "ended_reason": null | "queue_finished|stopped|replaced|taken_over|stopped_on_device|device_offline|handoff|failed",
  "queue": [{"video_id","title","channel","thumbnail_url","duration_s"}],
  "current_index": 0, "position_s": 123, "duration_s": 245, "volume": 40,
  "version": 7, "started_by": "<user_id>", "updated_at": "RFC3339"
}
```
Metadata của từng item (title, channel, thumbnail, duration) do service `cast` lấy từ catalog khi
tạo hoặc sửa hàng đợi. Client chỉ gửi `video_id`.

Lỗi:
- 404: không có phiên hoặc thiết bị.
- 409: `version` cũ, hoặc phiên đã kết thúc.
- 422: video là livestream. Server cũng phải chặn, không chỉ ẩn nút ở client.
- 503: thiết bị offline.

### 3.2 `GET /api/videos/{id}/audio` (gateway, loa tải audio qua đây)
- **Video có trên đĩa:** đảm bảo có `<mediaRoot>/<id>/audio.m4a`. Nếu chưa có thì chạy
  `ffmpeg -i 1080p.mp4 -vn -c:a copy -movflags +faststart <tmp>`, rồi `rename`.
  - Dùng `singleflight` theo video id để nhiều request cùng lúc chỉ chạy ffmpeg một lần.
  - Phục vụ file bằng `http.ServeContent` (hỗ trợ Range/206) với `Content-Type: audio/mp4`.
  - Lấy tên file mp4 từ cùng logic xác định file local mà `/stream` đang dùng (xem
    `media_presence.go`), đừng hard-code `1080p.mp4`.
- **Video chưa có trên đĩa:** proxy sang ingest **endpoint mới** `GET /audio/{id}`. Endpoint này
  nằm cạnh `remux.go`, chọn track adaptive chỉ có audio m4a/AAC (itag 140, fallback sang audio
  m4a tốt nhất), proxy theo **Range giới hạn ≤ 1 MiB mỗi request** (charter đã đo: track audio
  trả 206 khi ≤1 MiB và **403 khi ≥2 MiB**), và có retry 403 bằng cách resolve lại (copy mẫu ở
  `instant_retry_test.go` và `remux_retry_test.go`). Content-Type là `audio/mp4`.
  - Đồng thời kích hoạt tải video về như khi mở trang Watch: chỉ bài hiện tại, không tải cả hàng
    đợi (theo quy tắc một worker slot). Tôn trọng chế độ "Streaming only" (`cacheDisabled`): bật
    thì **không** lên lịch tải.
  - Video `UNAVAILABLE` (members-only, private, removed) thì trả 409 như `/stream`. Service `cast`
    gặp 409 thì bỏ qua bài đó.
- **Livestream:** trả 422.
- **Eviction:** catalog là nơi xoá file media (§4 Eviction). Kiểm tra code xoá: nếu nó xoá cả thư
  mục `<id>/` thì `audio.m4a` đi theo, nếu xoá theo tên file thì phải thêm `audio.m4a` vào. Logic
  "file biến mất mà không bị evict" (`handleStream` stat file) cũng phải áp dụng cho endpoint
  audio: kiểm tra root trước, root mất thì báo lỗi, **không** ghi gì. Ghi chú `audio.m4a` vào phần dung lượng
  ở `CLAUDE.md`.
- URL gửi cho loa phải là URL tuyệt đối mà loa truy cập được: `CAST_MEDIA_BASE_URL` (ví dụ
  `http://<IP LAN của Mac>:8180`), lấy từ env. **Không** dùng `localhost`.

### 3.3 Service `cast` (ConnectRPC, `proto/cast/v1/cast.proto`)
RPC tương ứng một-một với bảng REST ở §3.1:
`ListDevices`, `ListSessions`, `StartSession`, `GetSession`, `Pause`, `Resume`, `Next`, `Stop`,
`Seek`, `SetVolume`, `Jump`, `ReplaceQueue`, `Handoff`.
Chạy `make proto` để sinh code. `make check` phải pass.

Cấu trúc theo clean architecture (domain không import DB, HTTP hay framework):
```
services/cast/
  cmd/cast/main.go
  internal/domain/        session.go, queue.go, reconcile.go (+ _test.go)
  internal/usecase/       engine.go, commands.go (+ _test.go, fake driver/clock/repo)
  internal/adapter/
     postgres/            repo.go (+ integration test)
     r1/                  driver.go (+ httptest contract test)
     googlecast/          driver.go → castbridge HTTP (+ httptest test)
     catalog/             progress.go (ConnectRPC client)
     rpc/                 handler ConnectRPC
  migrations/0001_init.sql
```

Interface driver:
```go
type DeviceStatus struct {
    State      string // "idle"|"loading"|"playing"|"paused"
    MediaKey   string // R1: video_id; Google: content_id (URL) + app_id
    OwnedByUs  bool   // Google: app_id == "CC1AD845" && content_id == URL mình đã load
    PositionS  int
    DurationS  int
    Volume     int    // 0..100
    IdleReason string // Google: FINISHED|CANCELLED|INTERRUPTED|ERROR; R1: ""
}
type Driver interface {
    Kind() string
    Devices(ctx context.Context) ([]Device, error)
    Load(ctx context.Context, deviceID string, it Item, startS int, first bool) error
    Pause(ctx context.Context, deviceID string) error
    Resume(ctx context.Context, deviceID string) error
    Stop(ctx context.Context, deviceID string) error
    Seek(ctx context.Context, deviceID string, s int) error
    SetVolume(ctx context.Context, deviceID string, pct int) error
    Status(ctx context.Context, deviceID string) (DeviceStatus, error)
}
```

### 3.4 R1Driver → R1 + xiaozhi-server
`Load` gửi `POST {r1}/api/media/play`:
```json
{"items":[{
  "video_id":"abc", "title":"...", "artist":"<channel>", "thumbnail":"...", "duration":"4:05",
  "audio_url":"http://<mac>:8180/api/videos/abc/audio",
  "source":"mytube",
  "_seek": 0,
  "_silent": false
}], "start_index":0}
```
- `_silent` bằng `!first`: chỉ bài đầu tiên của phiên được đọc "Đang tải bài …".
- `Status` lấy từ `/api/media/state` (map `downloading` thành `loading`, `stopped` thành `idle`),
  cộng `volume` từ `/api/state`.
- Danh sách R1 đọc từ `data/cast-devices.json`:
  `[{"id":"r1-living","name":"Loa R1","url":"http://10.25.113.209:8088"}]`.
  `online` nghĩa là `/api/media/state` trả 200 trong vòng 1s.

**Sửa ở robot-esp32 (`play_youtube.py`):**
1. Item có `audio_url` thì **không** đi qua pytube. Dùng hàm mới `_download_url(song, cache_dir)`:
   - Tải về `cache_dir/mytube/<video_id>.m4a.part` (stream, có timeout).
   - Chạy `ffmpeg -i … -vn -c:a libmp3lame -q:a 4 <video_id>.mp3.part`, rồi `rename` thành
     `cache_dir/mytube/<video_id>.mp3`.
   - Có cache rồi thì dùng lại.
   - Lỗi thì trả `None` để luồng "tải lỗi, bỏ qua" hiện có xử lý.
   - Thư mục `mytube/` nằm ngoài pattern glob `*_{id}.mp3` của `_download`, nên không bị nhầm.
2. Phiên mà có item `source == "mytube"` thì **bỏ qua đoạn top-up `_related`**.
3. Không đổi gì khác. Luồng giọng nói và luồng panel R1 phải chạy y như cũ.
4. Theo skill `robot-service-logging` của repo đó (dùng logger sẵn có của xiaozhi-server).
5. Đọc `CLAUDE.md` của robot-esp32 trước khi sửa. Restart xiaozhi-server theo đúng cách của repo
   đó, **hỏi chủ nhà trước khi restart production**.

### 3.5 castbridge (Python, `services/castbridge/`, :8187) — P2
- `pychromecast` pin version trong `requirements.txt`, chạy trong venv riêng. **Kiểm tra lại tên API
  với đúng version đã cài** trước khi code (`CastBrowser`/`get_chromecasts`,
  `media_controller.play_media(url, content_type, title=, thumb=, current_time=)`, `seek`, `pause`,
  `play`, `stop`, `set_volume`, `status.app_id`, `media_controller.status.idle_reason`).
- Endpoint:
  - `GET /devices` → `[{uuid, name, model, cast_type, host}]`. **Lọc bỏ `cast_type == "cast"`**
    (TV), giữ lại `audio` và `group`.
  - `POST /devices/{uuid}/load {url, content_type:"audio/mp4", title, thumbnail, start_s}`
  - `POST /devices/{uuid}/pause|play|stop`, `POST /devices/{uuid}/seek {s}`,
    `POST /devices/{uuid}/volume {level 0..1}`
  - `GET /devices/{uuid}/status` →
    `{player_state, content_id, app_id, current_time, duration, idle_reason, volume_level}`
- Log theo định dạng Python `logging` mà logview đọc được (theo `translate_server.py`). Ghi vào
  `LOG_DIR/castbridge.log`.
- `GET /health` trả `local-mytube-castbridge`. `dev.sh` phải **hỏi danh tính** port 8187 như đang
  làm với transcript helper (`local-mytube-transcript`), không coi "port đang mở" là service của
  mình. Chỉ listen `127.0.0.1`.
- Discovery chạy liên tục. Thiết bị biến mất quá 60s thì bỏ khỏi danh sách.

### 3.6 DB `cast` (migration `0001_init.sql`, thêm `'cast'` vào vòng lặp trong `db/bootstrap.sql`)
```sql
CREATE TABLE cast.sessions (
  id uuid PRIMARY KEY,
  device_id text NOT NULL,
  device_kind text NOT NULL,
  started_by text NOT NULL,          -- user_id
  queue jsonb NOT NULL,              -- [{video_id,title,channel,thumbnail_url,duration_s}]
  current_index int NOT NULL,
  position_s int NOT NULL DEFAULT 0,
  state text NOT NULL CHECK (state IN ('loading','playing','paused','ended')),
  ended_reason text,
  loaded_media_key text,             -- cái mình đã Load lên loa (để phát hiện bị chiếm)
  loading_since timestamptz,
  last_seen_ok timestamptz,
  last_progress_at timestamptz,
  version int NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX sessions_one_active_per_device ON cast.sessions(device_id) WHERE state <> 'ended';
```

---

## 4. Logic lõi: `Reconcile(session, status, err, now) → []Action` (hàm thuần)

Engine chạy 1 vòng lặp mỗi 1s cho **từng** phiên chưa `ended`, kể cả khi không có client nào mở.
Khi service khởi động, engine đọc lại các phiên đang chạy và **tiếp tục theo dõi**, không Load lại
bài đang phát. Mọi lệnh từ client (pause, seek, …) đi qua cùng engine, có khoá theo phiên, để không
chạy đua với vòng lặp.

Các quy tắc dưới đây xét **theo thứ tự**:

| # | Điều kiện | Hành động |
|---|---|---|
| 1 | `Status` lỗi liên tục **> 30s** | `end(device_offline)` |
| 2 | Thiết bị đang phát thứ **không phải của mình**. R1: `video_id` khác `loaded_media_key` và khác null. Google: `!OwnedByUs`. | `end(taken_over)`. **Không** gửi lệnh nào xuống loa. |
| 3 | `state=loading` và `now - loading_since > 90s` | Đánh dấu bài lỗi, `advance()`. Nếu là bài cuối thì `end(failed)`. |
| 4 | Loa `idle` sau khi phiên đã `playing` bài hiện tại, **và** bài đã hết. R1: `position_s ≥ duration_s - 5` ở lần quan sát cuối. Google: `IdleReason == FINISHED`. | `recordProgress(final)`, rồi `advance()`. Hết hàng thì `end(queue_finished)` và `Stop()`. |
| 5 | Loa `idle` nhưng bài **chưa** hết (người dùng nói "dừng nhạc" với R1, hoặc tắt từ app Google Home) | `end(stopped_on_device)` |
| 6 | Loa `playing` hoặc `paused` đúng bài của mình | Cập nhật `state`, `position_s`, `volume`. Đã ≥10s kể từ `last_progress_at` thì `recordProgress`. |
| 7 | R1 `playing` nhưng `position_s` đứng yên (đang trả lời câu hỏi bằng giọng nói) | **Không** coi là bị chiếm hay bị dừng. Chỉ giữ nguyên trạng thái. |

- `advance()` gọi `Load(next, startS=0, first=false)`, đặt `state=loading`, `loading_since=now`,
  rồi `version++`.
- `StartSession` gọi `Load(items[start_index], start_position_s, first=true)`. Nếu thiết bị đang có
  phiên khác thì phiên cũ `end(replaced)` trong **cùng transaction**.
- `ReplaceQueue` cần `version` khớp. Nếu bài hiện tại vẫn còn trong hàng mới thì chỉ cập nhật
  `current_index`, **không** Load lại. Nếu bài hiện tại bị xoá thì Load bài ở `current_index`.
- `Handoff` gọi `Stop()`, đọc `position_s` cuối cùng, `end(handoff)`, rồi trả về
  `{video_id, position_s}`.
- Lệnh `Stop` của client gọi `Stop()` rồi `end(stopped)`.

---

## 5. Các bước làm

### P1 — lõi + R1 + web
1. **robot-esp32**: làm §3.4 (`audio_url`, `source=mytube` tắt bài liên quan) kèm test ở §6.1.
2. **ingest**: thêm `GET /audio/{id}` (§3.2) kèm test.
3. **gateway**: thêm `GET /api/videos/{id}/audio` (§3.2) kèm test.
4. **proto + service `cast`**: proto, domain/Reconcile, engine, repo Postgres, R1Driver, client
   catalog, handler ConnectRPC, migration, bootstrap, dev.sh/stop.sh, logview đọc
   `cast.log`.
5. **gateway**: REST `/api/cast/*` (§3.1) chuyển tiếp sang `cast`.
6. **web** (`src/features/cast/`):
   - `domain`: kiểu `Device`, `SessionView`.
   - `infrastructure`: `castRepository` gọi `/api/cast/*` **qua `shared/api/http.ts`** (nơi duy
     nhất được gọi `fetch`, và là nơi gắn header `X-User-Id`). `infrastructure/` không dịch chữ,
     chỉ ném lỗi kèm code.
   - `application`:
     - `useCastDevices()`: chỉ gọi khi mở picker.
     - `useActiveCastSession()`: poll `/api/cast/sessions` mỗi 5s ở mọi trang, mỗi 1s khi remote
       mở. Dừng poll khi tab ẩn (`visibilitychange`).
     - `useCastControls(id)`.
     - `startCast(device)`: pause player, lấy `currentTime`, video hiện tại và hàng tiếp theo/
       playlist (dùng `queue.ts` / up-next sẵn có), rồi `POST`.
     - `handoff()`: gọi API, mở Watch cho `video_id` và seek tới `position_s`.
   - `ui`:
     - `CastButton` trong thanh điều khiển của `Player.tsx`. Ẩn khi video là live **hoặc** không
       có thiết bị online.
     - `DevicePicker` (sheet hoặc popover; trên phone là bottom sheet).
     - `CastRemote`: thay chỗ player khi đang cast. Có ảnh bìa, tiêu đề, `SeekBar` sẵn có, play/
       pause, next, stop, slider âm lượng, danh sách hàng đợi (kéo để đổi thứ tự, xoá, chạm để
       nhảy tới), nút "Ngắt cast".
     - `CastMiniBar`: thanh nhỏ toàn cục khi có phiên đang chạy, ở cả trang Home.
     - Khi phiên kết thúc với lý do `taken_over`, `stopped_on_device`, `device_offline`, `failed`
       hoặc `replaced`: hiện toast hoặc banner (i18n) rồi đóng remote.
   - i18n: thêm key **cả en lẫn vi**. Ba lớp guard của charter §4b (typed keys,
     `dictionaries.test.ts`, `untranslated.guard.test.ts`) phải xanh. "Cast" là thuật ngữ, giữ
     tiếng Anh. Test chạy bằng tiếng Anh (`test/setup.ts`).
7. Cập nhật `CLAUDE.md` (mục mới "Cast", kèm số đo) và `CHANGELOG.md` của myTube.

### P2 — loa Google
1. **Spike trước (≤ 1 giờ):** dùng script pychromecast phát `http://<mac>:8180/api/videos/<id>/audio`
   lên Nest Mini. Đo thời gian tới khi có tiếng, seek, và xem `idle_reason` khi hết bài hoặc khi
   bấm stop trong Google Home. Ghi số đo vào changelog. Nếu Nest không phát được URL `http://` thì
   **dừng lại hỏi chủ nhà**.
2. Làm castbridge (§3.5), thêm vào dev.sh/stop.sh.
3. Làm GoogleDriver trong `cast`, map `IdleReason` sang quy tắc 4/5. Thêm `cast_type=group` vào
   danh sách thiết bị.
4. Web không phải đổi gì ngoài icon theo `kind`.

### P3 — app Android/iOS (`mytube-app`)
1. `commonMain`: `CastRepository` gọi gateway, `CastSessionStore` (poll 1s khi remote mở, 5s khi
   app ở foreground, **ngưng** khi app ở background).
2. UI Compose:
   - Nút cast trên player (ẩn với live, ẩn khi không có thiết bị online).
   - Picker dạng bottom sheet.
   - Màn hình remote và mini bar.
   - Không làm cho bố cục TV (`ui/tv/` không có nút này).
3. Bấm cast thì **pause `VideoPlayer`** rồi gửi `currentPosition`. Handoff thì `VideoPlayer` phát
   tiếp từ `position_s`.
4. Theo các quy tắc trong `CLAUDE.md` §11 và changelog của app (tìm "player" và "background" trước
   khi sửa player).

---

## 6. QA

### 6.1 Test tự động (bắt buộc, viết test **trước** khi sửa, theo TDD)

**robot-esp32** (`xiaozhi-server/tests/test_play_youtube_mytube.py`, pytest):
- [ ] Item có `audio_url`: tải từ URL (mock `requests`) và convert thành mp3 bằng **ffmpeg thật**.
  Dùng fixture m4a sinh bằng `ffmpeg -f lavfi -i sine=d=3 -c:a aac`. File nằm ở
  `cache/mytube/<id>.mp3`, ffprobe đo ra khoảng 3s.
- [ ] Lần thứ hai không tải lại (đếm số lần gọi mock).
- [ ] URL trả lỗi 500 thì trả `None` và không để lại file `.part`.
- [ ] Phiên có `source=mytube` **không** gọi `_related`, kể cả khi hàng còn dưới 2 bài.
- [ ] Phiên giọng nói (không có `audio_url`) **vẫn** gọi `_related`. Đây là test regression.
- [ ] `_seek=60` vẫn đi qua `_trim_mp3`. `_silent=true` không đọc câu thông báo.
- [ ] Chạy lại toàn bộ test có sẵn, gồm `tests/test_turn_guard_cleanup.py`. Tất cả phải pass.

**ingest** (`go test ./...`):
- [ ] `/audio/{id}` chọn đúng track audio m4a, request upstream **luôn có Range giới hạn**
  (assert header), và gặp 403 thì resolve lại rồi thử thêm một lần.

**gateway**:
- [ ] Video local: lần gọi đầu tạo `audio.m4a`. Mười request song song chỉ chạy ffmpeg **1 lần**
  (đếm bằng fake runner).
- [ ] Gửi `Range: bytes=100-199` thì nhận 206 với đúng 100 byte, `Content-Type: audio/mp4`.
- [ ] Video không có trên đĩa thì proxy sang ingest (dùng ingest giả bằng httptest).
- [ ] Video live thì trả 422.
- [ ] Các handler REST `/api/cast/*`: map đúng RPC, mã lỗi đúng (404/409/422/503) và lấy
  `user_id` từ `g.userID`.

**cast/domain** (`reconcile_test.go`, table-driven, đủ cả 7 quy tắc ở §4 và các biên):
- [ ] Hết bài tự nhiên thì `advance`. Hết hàng thì `queue_finished` kèm `Stop`.
- [ ] Idle khi bài chưa hết (R1 `position 30/245`) thì `stopped_on_device`.
- [ ] R1 đổi `video_id` sang bài khác thì `taken_over`, và **không** có action nào gửi xuống loa.
- [ ] Google `app_id` khác, hoặc `content_id` khác, thì `taken_over`.
- [ ] Google `IdleReason=FINISHED` thì `advance`. `CANCELLED` thì `stopped_on_device`.
- [ ] `playing` mà position đứng yên 40s (đang trả lời giọng nói) thì không có action.
- [ ] Lỗi Status 29s thì chưa làm gì. Đến 31s thì `device_offline`.
- [ ] `loading` quá 90s thì skip. Nếu là bài cuối thì `failed`.
- [ ] Mỗi 10s mới `recordProgress` một lần, không phải mỗi tick.

**cast/usecase** (fake driver + fake clock + fake repo):
- [ ] `StartSession` trên thiết bị đang bận: phiên cũ `replaced`, phiên mới `loading`, chỉ có 1 phiên
  hoạt động (unique index).
- [ ] `ReplaceQueue` với version cũ trả 409. Đổi thứ tự mà giữ bài hiện tại thì **không** Load lại.
  Xoá bài hiện tại thì Load bài mới.
- [ ] `Handoff` trả về đúng `position_s` và phiên `ended(handoff)`.
- [ ] Restart: tạo engine mới từ repo đang có phiên `playing` thì nó tiếp tục theo dõi và không gọi
  `Load`.
- [ ] Lệnh client chạy cùng lúc với tick không làm hỏng state (chạy `go test -race`).

**cast/adapter**:
- [ ] R1Driver chạy với httptest giả lập R1:
  - Body `/api/media/play` đúng §3.4 (`audio_url`, `source`, `_seek`, `_silent=!first`).
  - Map state đúng.
  - Đọc volume từ `/api/state`, đặt bằng `/api/set?key=volume&value=`.
  - Timeout 1s thì `online=false`.
- [ ] GoogleDriver chạy với httptest giả lập castbridge (P2).
- [ ] Repo Postgres: test integration theo mẫu các service khác (unique index, version, jsonb queue).

**castbridge** (pytest, mock pychromecast) — P2:
- [ ] `/devices` lọc bỏ `cast_type=cast`.
- [ ] `/load` gọi `play_media` với đúng tham số.
- [ ] `/status` map đúng field.

**web** (vitest + Testing Library):
- [ ] `CastButton` ẩn khi video live, ẩn khi danh sách thiết bị rỗng, và hiện khi có ít nhất 1
  thiết bị.
- [ ] `startCast`: pause player, gửi `start_position_s` bằng `currentTime` làm tròn, gửi items là
  video hiện tại cộng hàng tiếp theo.
- [ ] Poll 1s khi remote mở, 5s khi đóng, dừng khi tab ẩn (dùng fake timers).
- [ ] Phiên trả về `taken_over` thì hiện banner đúng key i18n và remote đóng.
- [ ] `handoff`: điều hướng tới Watch và seek tới `position_s`.
- [ ] Sửa hàng đợi bị 409 thì tải lại phiên và báo cho người dùng.
- [ ] Test i18n guard sẵn có vẫn pass (không thiếu bản vi).
- [ ] Playwright e2e (mock route `/api/cast/*`): bấm cast, chọn R1, thấy remote, seek, next,
  "Ngắt cast" rồi player phát lại.

**app** (`commonTest`/`jvmTest`) — P3:
- [ ] Store: nhịp poll, ngưng khi ở background, map trạng thái kết thúc sang thông báo.

**Cổng chung:** `make check` (buf lint, tsc, go build), `go test -race ./...` trong mọi Go service
bị sửa, `npm test` trong `web/`, pytest ở robot-esp32. **Tất cả phải xanh** mới được báo xong.

### 6.2 Test tay trên thiết bị thật (sau mỗi phase, ghi kết quả và số đo vào CHANGELOG)

Chuẩn bị: R1 đang online. Một video **đã tải** (A), một video **chưa tải** (B), một playlist có ≥3
bài, và một video đang live (L). Hai điện thoại (P1, P2).

| # | Bước | Kỳ vọng |
|---|---|---|
| 1 | Mở A trên P1, xem tới 1:30, bấm cast, chọn "Loa R1" | Video trên P1 dừng, remote hiện ra. R1 đọc "Đang tải bài …" rồi phát **từ khoảng 1:30** (±3s). **Đo thời gian bấm → có tiếng.** |
| 2 | Tắt hẳn app hoặc tab trên P1 | R1 vẫn phát tiếp. |
| 3 | Mở lại P1 | Có mini bar hoặc remote đúng bài, đúng vị trí (±2s). |
| 4 | Mở myTube trên P2 | P2 cũng thấy phiên và điều khiển được (pause trên P2 thì P1 thấy paused trong ≤2s). |
| 5 | Kéo thanh seek tới 3:00 | R1 phát từ 3:00 trong ≤5s, không đọc lại thông báo. |
| 6 | Pause rồi Resume | Đúng trạng thái, phát tiếp đúng chỗ. |
| 7 | Chỉnh âm lượng 20% → 60% | R1 to lên. Slider khớp với nấc thật (15 nấc). |
| 8 | Cast một playlist 3 bài, bấm Next | Sang bài 2, **không** đọc "Đang tải bài". |
| 9 | Đổi thứ tự bài 3 lên trước, rồi xoá một bài | Hàng đợi trên P1 và P2 giống nhau. Bài đang phát không bị ngắt. |
| 10 | Để phát hết bài cuối | R1 dừng, **không** tự phát bài liên quan. Phiên `queue_finished`. **Đo khoảng lặng giữa hai bài.** |
| 11 | Đang cast thì gọi wake word và hỏi "mấy giờ rồi" | R1 trả lời rồi phát tiếp. Remote **không** báo bị chiếm. |
| 12 | Đang cast thì nói "phát bài Sơn Tùng" | R1 phát bài đó. Remote báo "Loa đã chuyển sang nguồn khác" và đóng. |
| 13 | Đang cast thì nói "dừng nhạc" | Phiên `stopped_on_device`, remote báo và đóng. |
| 14 | Cast B (chưa tải) | Có tiếng trong ≤15s (**đo**). Video B bắt đầu được tải về (xem Storage/ingest). |
| 15 | Mở L (live) | **Không** có nút cast. Gọi API trực tiếp thì nhận 422. |
| 16 | Đang phát thì restart stack myTube (`launchctl kickstart -k gui/$(id -u)/com.luke.local-mytube`) | Bài hiện tại vẫn phát. Hết bài thì vẫn sang bài kế. Remote vẫn chạy. |
| 17 | Rút điện R1 | Sau khoảng 30s phiên `device_offline`, remote báo. R1 biến mất khỏi danh sách thiết bị. |
| 18 | Bấm "Ngắt cast" ở 2:10 | R1 dừng. P1 phát video tiếp từ khoảng 2:10. |
| 19 | Mở History của profile vừa cast | Có A, tiến độ đúng khoảng chỗ đã nghe tới. |
| 20 | P1 cast A lên R1, P2 cast C lên R1 | Phiên của P1 `replaced`, R1 phát C. |
| 21 | **Regression R1:** dùng panel `:8088` tab Media và lệnh giọng nói "mở bài …" như trước | Vẫn chạy y như cũ, kể cả tự thêm bài liên quan. |

**P2 bổ sung** (lặp lại bước 1–10, 14, 16–20 trên **Nest Mini** và **Nest Audio**), thêm:

| # | Bước | Kỳ vọng |
|---|---|---|
| 22 | Danh sách thiết bị | Có R1, Nest Audio, 2 Nest Mini. **Không** có Chromecast TV. Có nhóm loa nếu đã tạo trong Google Home. |
| 23 | Đang cast thì phát Spotify hoặc YouTube Music lên Nest từ app khác | `taken_over`. |
| 24 | Bấm stop trong app Google Home | `stopped_on_device`. |
| 25 | Cast lên một nhóm loa | Cả nhóm cùng phát. Điều khiển chạy như một thiết bị. |

**P3 bổ sung** (Android và iOS): lặp lại bước 1–4, 5–9, 18, 20. Thêm:
- Khoá màn hình hoặc đưa app vào background thì app **ngưng poll**. Kiểm tra bằng log gateway:
  không có request `/api/cast/sessions` nào trong lúc đó.
- TV build **không** có nút cast.

### 6.3 Tiêu chí nghiệm thu (đo rồi ghi lại, đừng bịa số)
- R1, video đã tải: từ lúc bấm tới khi có tiếng **≤ 10s**.
- Nest, video đã tải: **≤ 5s**.
- Video chưa tải: **≤ 15s**.
- Khoảng lặng giữa hai bài trên R1: **≤ 5s**.
- Remote lệch so với loa **≤ 2s**.
- Không mục nào ở §6.2 bị fail. Nếu có mục fail thì ghi rõ vào báo cáo, không giấu.

---

## 7. Rủi ro và điểm cần đo sớm
1. **Nest phát URL `http://` trên LAN.** Spike ở đầu P2 sẽ trả lời. Nếu không được thì dừng lại hỏi
   (có thể phải phục vụ qua Caddy TLS).
2. **R1 phải tải hết file mp3 rồi mới phát**, nên bài dài và chưa tải sẽ chậm. Đo ở bước 14. Nếu
   vượt 15s thì báo lại, **không** tự đổi kiến trúc.
3. **Phân biệt "hết bài" với "bị dừng" trên R1** dựa vào `position_s ≥ duration_s - 5`. R1 đẩy
   position theo chu kỳ, nên nếu chu kỳ thưa hơn 5s thì phải nới ngưỡng. Đo trước rồi mới chốt.
4. `audio.m4a` làm tăng dung lượng đĩa (khoảng 3–4 MB mỗi video có cast). Ghi vào phần storage
   của `CLAUDE.md`.
5. R1 chỉ có một phiên phát. Phiên nhạc gọi bằng giọng nói luôn đè lên phiên cast, đúng với quyết
   định "ai sau thắng".

## 8. Quy tắc cho người làm
- Code, comment và commit viết **tiếng Anh**. Báo cáo cho chủ nhà viết tiếng Việt.
- Chỉ commit khi chủ nhà bảo. Không push.
- Không tạo LaunchAgent mới. Không restart service production (xiaozhi-server, stack myTube) khi
  chưa hỏi.
- Không bịa API. pychromecast, ffmpeg flag và proto đều phải kiểm tra trên bản đã cài hoặc trong
  source.
- Mỗi phase xong thì báo cáo: những gì đã làm, output test (đã che secret), bảng §6.2 có cột
  pass/fail, và số đo ở §6.3.
