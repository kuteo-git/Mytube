# Narration System — Handoff Document

## Tổng quan

Hệ thống thuyết minh (narration) tự động đọc phụ đề tiếng Việt/Anh bằng giọng TTS, đồng bộ với video YouTube đã tải về.

**2 chế độ:**
- **VI sub có sẵn** → đọc trực tiếp
- **EN sub (không có VI)** → dịch qua NLLB-200 → đọc tiếng Việt

---

## Kiến trúc pipeline

```
1. VTT file (fetch từ /media/)
   │
2. fetchAndParseVTT()
   ├── Auto-caption (có <c> tags): parse word-level timestamps → sub-cues
   └── Manual caption (ko <c> tags): cues nguyên bản, join multi-line
   │
3. Grouping (chỉ auto-caption)
   ├── Gom text, split tại . ! ? ,
   └── stripBrackets() sau khi group
   │
4. TTS Scheduling (tickNarration, 60fps)
   ├── Translate EN→VI (nếu cần)
   ├── 2-pass speed fitting (DEFAULT_SPEED → MAX_SPEED)
   └── Play qua Web Audio API
```

---

## File chính

| File | Vai trò |
|------|---------|
| `web/src/features/watch/application/narration.ts` | Parser VTT + scheduler TTS |
| `web/src/features/watch/ui/Player.tsx` | UI button + AudioContext management |
| `services/gateway/internal/api/tts.go` | Proxy TTS + ffmpeg atempo |
| `services/gateway/internal/api/translate.go` | Proxy NLLB-200 |
| `services/translate_server.py` | Translation server: Omniroute + Qwen + NLLB (port 8005) |

---

## Các hằng số quan trọng

| Constant | Value | Ý nghĩa |
|----------|-------|---------|
| `TTS_VOICE` | `'Ngọc Linh'` | Giọng đọc |
| `DEFAULT_SPEED` | `1.1` | Tốc độ mặc định (server atempo) |
| `MAX_SPEED` | `3.0` | Tốc độ tối đa khi slot chật |
| `PREFETCH_SEC` | `60` | Fetch TTS trước bao nhiêu giây (đã đổi từ 10 — xem CLAUDE.md §8.3b: 10s runway làm narration im sau ≤10s khi tab vào nền) |
| `GAP_BETWEEN_CLIPS` | `0.25` | Khoảng nghỉ giữa 2 câu |
| `NARRATION_DUCK` | `0.2` | Volume video khi bật thuyết minh (20%) |
| `MAX_WORDS_NO_PUNCT` | `30` | Force split nếu ko có dấu câu |
| `WARM_START_SKIP` | `10s` | Skip 10s đầu khi mới bật |

---

## Luật parse VTT

### Auto-caption (YouTube generated)

Đặc điểm: có `<c>` tags, format progressive accumulation.

**Parse word-level timestamps:**
```
Được <00:00:00.155><c>rồi, </c><00:00:00.310><c>tôi </c>...
→ sub-cues: "Được"@0.000, "rồi,"@0.155, "tôi"@0.310, ...
```

**Leading text** trước tag đầu tiên được giữ (vd: `"Được "`).

**2-line carry-over KHÔNG có `<c>` tags:** dòng 1 = text cũ (bỏ), dòng 2 = text mới (lấy).

**10ms clean snapshot:** filter (`end - start < 0.1`).

**Leading whitespace:** skip blank lines nhưng ko bao giờ skip timing line (`!lines[i].includes('-->')`).

**Clause grouping** (chỉ auto-caption):
| Punctuation | Rule |
|-------------|------|
| `.` | Split, TRỪ decimal (`2.5`) và abbreviation (`Dr.`, `Mr.`, `DR.`) |
| `!` `?` | Luôn split |
| `,` | Split khi **cả 2 bên** > 2 từ. Ko split nếu 1 bên ≤ 2 (tránh orphan `"Then,"` hay `", maybe"`) |

**Timing:** clause end = start của từ cuối (ko phải next word's start).
**Brackets:** `[tiếng vỗ tay]`, `[âm nhạc]` bị strip sau khi group. Emotion tags `[cười]`, `[thở dài]` được giữ.

### Manual caption (human-made)

Đặc điểm: ko có `<c>` tags, mỗi cue là 1 câu hoàn chỉnh.

**Multi-line cues:** JOIN tất cả dòng (ko dùng `isTwoLineCarry`).
**2-line carry-over:** KHÔNG áp dụng cho manual.
**Grouping:** BỎ QUA — manual cues đã là câu hoàn chỉnh.
**Brackets:** Strip tất cả `[...]` (speaker labels, sound effects).

---

## TTS Speed Fitting

```
slot = max(0.1, end - start)
IF next_cue có gap: slot = min(next_cue.start - start, slot × 2)

Fetch ở DEFAULT_SPEED (1.1×) → buf.duration

IF buf.duration > slot:
   natural = buf.duration × DEFAULT_SPEED
   needed  = min(natural / slot, MAX_SPEED)
   IF needed > DEFAULT_SPEED + 0.02: re-fetch ở needed
```

---

## You/Your Pronoun Replacement

NLLB dịch `you` → `anh` (formal). Pipeline thay thế:

1. Trước translate: `you're` → `XXXX are`, `you'll` → `XXXX will`, `you` → `XXXX`, `your` → `YYYY`, v.v.
2. Dịch qua NLLB
3. Sau translate: `XXXX` → `bạn`, `YYYY` → `của bạn`

Full list: `you're`, `you'll`, `you'd`, `you've`, `yourself`, `yourselves`, `you`, `yours`, `your`. `\b` boundary chống false positive (`young`, `youth`).

---

## Audio Pipeline

- **Server-side atempo:** Gateway nhận `speed` param → ffmpeg `atempo` (pitch-preserving) → trả WAV đã speed
- **Cache:** key = `text@@speed` (cùng text khác speed = cache entries khác nhau)
- **Playback:** Browser play ở 1.0×, fade-in/out 50ms mỗi clip
- **Ducking:** Clip trước fade-out 80ms khi clip sau bắt đầu
- **Volume:** TTS theo `video.volume × 2.5`, video ducked còn 20% (`NARRATION_DUCK`)

---

## State Persistence (localStorage)

| Key | Ý nghĩa |
|-----|---------|
| `yt-narration-output-v1` | `off` / `subs` / `voice` / `both` |
| `yt-narration-engine-v1` | `omniroute` (mặc định) / `qwen` / `nllb` |
| `yt-narration-on` | **Cũ.** Boolean bật/tắt; chỉ còn được đọc một lần để chuyển `'1'` → `output: 'voice'` |
| `yt-player-volume` | Volume người dùng |
| `yt-player-muted` | Mute state |
| `yt-player-captions` | Ngôn ngữ sub đang chọn |

---

## Các bug đã fix (đáng nhớ)

1. **Leading blank line ăn timing line:** `while (trim === '')` skip quá xa → thêm guard `!includes('-->')`
2. **Empty payload advance i:** payload rỗng vẫn `i++` → mất timing line tiếp → chỉ `i++` khi có payload
3. **2-line carry-over ko `<c>` tags:** `isTwoLineCarry` nhưng dòng 2 chỉ là `"?"` hoặc `"giúp đỡ."` → fallback: dùng text as-is
4. **`isTwoLineCarry` áp dụng cho manual:** manual 2-line cue `["In the 20th century,", "it was oil;"]` bị mất dòng 1 → chỉ dùng cho auto-caption
5. **`bufEnd = cue.start` với manual:** manual cue có real duration → `end - start > 0.5` thì dùng `cue.end`
6. **`[tiếng vỗ tay]` span 2 `<c>` tags:** `[tiếng` và `vỗ tay]` → strip brackets sau khi group
7. **`K3.` period preceded by digit:** regex `[a-zA-Z][.!?,]` ko match → thay bằng manual check (digit.digit pattern)
8. **AudioContext null khi refresh:** `narrationOn` từ localStorage nhưng ko ai click → tạo AudioContext trong useEffect
9. **onVolumeChange feedback loop:** ducking set volume → fire event → set state → re-duck → thêm `lastSetVolumeRef` guard

---

## Translation Models

Config: `services/translate_server.py`. Port 8005. **Ba** engine, chọn trong menu
⚙ của player dưới mục "Máy dịch".

### Omniroute (mặc định, "Tốt nhất")

Router OpenAI-compatible trong mạng LAN. Cấu hình **hoàn toàn từ biến môi
trường**, key **không nằm trong git** — `scripts/dev.sh` nạp `.env.local`:

```
OMNIROUTE_BASE_URL  http://10.25.113.151:20128
OMNIROUTE_MODEL     sub_translation      (router xuống deepseek-v4-flash)
OMNIROUTE_API_KEY   …                    (.env.local, đã gitignore)
```

**`stream: false` là bắt buộc, không phải tuỳ chọn** — server này stream mặc
định kể cả khi không ai xin, và một thân SSE thì không phải JSON.

Chỉ đọc `content`, **bỏ `reasoning_content`**: model này là reasoning model, và
phần nó tự nói với chính mình đôi khi cũng chứa dòng đánh số trông y như bản
dịch.

Đo trên video thật (150 cue): lô đầu 3 câu **4.9s**, lô 15 câu **10–16s**,
6.3 từ/giây, khớp dòng 15/15 không phải fallback lần nào. **Nhanh hơn Qwen ở câu
đầu** vì không phải nạp model, và **không đụng GPU** — thứ mà yt-dlp với ffmpeg
đã giành sẵn mỗi lần bấm play.

Mọi số đo trên máy này (M4, 24 GB) ngày 2026-08-03, dùng cue đã gộp bởi
`parseVTT()` thật.

| | NLLB-200-distilled-600M | Qwen3-8B-4bit (MLX) |
|---|---|---|
| Đường đi | 1 request/cue | lô 15, kèm 3 cue ngữ cảnh |
| Throughput | ~10 từ/giây | 7.43 từ/giây |
| Mỗi cue | 1.34s (20 từ), 1.71s (17 từ) | 1.36s |
| Lần gọi đầu | 13.3s (cold) | 3.5s load model |
| RAM | ~4 GB | ~4.5 GB |
| Gọi người xem là "bạn" | chỉ khi có hack marker (không hack: sai 2/5) | 5/5 từ prompt |
| Ngữ cảnh giữa các cue | không | có |

**MLX không chạy được NLLB.** NLLB-200 là encoder-decoder (M2M100), mà `mlx-lm`
chỉ có kiến trúc decoder-only — đã kiểm cả 120 file trong `mlx_lm/models/`.
Muốn port thì phải tự viết model. Đây là *đã kiểm*, không phải phỏng đoán.

**Đổi engine KHÔNG làm dịch nhanh hơn.** Cả hai ứng viên MLX đều đo được **chậm
hơn** NLLB tính trên mỗi cue. Toàn bộ phần nhanh đến từ cache trong
`{MEDIA_ROOT}/{videoId}/narration.vi.json`; đổi engine là mua **chất lượng**.
Đừng với tay sang model to hơn để chữa một than phiền về độ trễ.

**Lệch dòng theo lô là do đầu vào vụn, không phải do model.** Cho ăn dòng VTT
thô đã khử trùng lặp: Qwen trả 9/12 dòng, Gemma-3-12B trả 44/60 — và vì client
map theo vị trí, một dòng thiếu **không** làm mất một cue mà làm **mọi cue sau
đó bị đọc lệch nhịp**, âm thầm. Cho ăn cue gộp bởi `parseVTT()`: cả hai đều
60/60. Mọi benchmark sau này phải dùng cue đã gộp, không thì đo nhầm thứ.
Server vẫn từ chối lô có số dòng không khớp và dịch lại từng cue.

**Gemma-3-12B đã đo và đã loại.** Tiếng Việt tự nhiên nhất trong ba, nhưng nó
**bịa thêm nội dung**: `"Just think about that."` trả về `"Nghĩ xem, đúng là bất
ngờ."` Người nghe thuyết minh không có cách nào phân biệt câu bịa với câu người
dẫn thật sự nói. Ngoài ra chậm hơn 1.7× (4.32 từ/giây), load 33s, cần 7 GB —
không vừa ổ trong, chỉ nằm được trên SSD ngoài.

**Đo end-to-end qua gateway thật (2026-08-03), video `Ersv1ogj7Jo`, resume ở
giây 120 → bắt đầu từ cue 35 (bắt đầu lúc 115.2s, đúng playhead):**

| | |
|---|---|
| Lô 1 (3 cue), server đã ấm | **3.0s** → câu đầu có tiếng |
| Lô 1 (3 cue), lần gọi Qwen **đầu tiên của process** | **31.5s** — phải load model |
| Lô 15 cue | 11.5–15.8s |
| Lượt 2 cùng video | **0 request**, 4/4 lô đọc từ cache |

Cái 31.5s là lý do `dev.sh` nên được để chạy chứ đừng restart giữa buổi xem.
Model load một lần cho cả đời process.

**Lô lệch dòng vẫn xảy ra dù cue đã gộp** — đo được 1 lần trong 8 lô. Server bắt
được, dịch lại từng cue, trả đủ 15/15, client không thấy gì bất thường. Đó là
lý do đường fallback tồn tại, không phải phòng xa thừa.

**Mật độ lời nói**, đo trên 6 file phụ đề thật: **1.1–3.1 từ/giây video**. Nên
Qwen đi trước playhead 2.4–3.7×, NLLB còn hơn. Biên đó không vô hạn: bấm play
cũng khởi động một lượt tải yt-dlp và một lượt remux ffmpeg giành cùng GPU.

`narration-vtt.ts` chạy được nguyên xi bằng `node --experimental-strip-types`
(Node 25), nên nếu sau này muốn dịch sẵn phía server thì **không cần port**
parser 348 dòng sang Python.

### Cách đổi model

1. Sửa `QWEN_MODEL` (hoặc `NLLB_MODEL`) trong `services/translate_server.py`
2. Restart: `/tmp/nllb-venv/bin/python services/translate_server.py &`
3. Cache phân vùng theo **id engine** — model mới mà dùng lại id cũ sẽ đọc phải
   bản dịch của model trước. Đặt id mới.

## Unit Tests

`narration.test.ts` — 29 tests:

- **Grouping logic:** clause splitting, comma rules, abbreviation handling (7 tests)
- **2-line carry-over:** `?`, `giúp đỡ.`, `béo.` fallback (3 tests)
- **Word-level timing:** leading text, precise timestamps (2 tests)
- **Warm-start skip:** initial 10s, seek detection (3 tests)
- **Pronoun replacement:** you/your variants, contractions, round-trip (6 tests)
- **Bracket stripping:** auto vs manual (2 tests)
- **E2E VTT file:** MeplqZ0nM1c auto-caption (4 tests)

Run: `npx vitest run web/src/features/watch/application/narration.test.ts`

---

## Các service cần chạy

| Port | Service | Start command |
|------|---------|---------------|
| 8180 | Gateway | `scripts/dev.sh` |
| 8002 | TTS (VieNeu) | `python robot-esp32/services/vieneu_server.py` |
| 8005 | Translate (NLLB + Qwen) | `/tmp/nllb-venv/bin/python services/translate_server.py` |
| 5173 | Vite (web) | `scripts/dev.sh` (auto) |
