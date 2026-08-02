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
| `services/nllb_server.py` | NLLB-200 translation server (port 8005) |

---

## Các hằng số quan trọng

| Constant | Value | Ý nghĩa |
|----------|-------|---------|
| `TTS_VOICE` | `'Ngọc Linh'` | Giọng đọc |
| `DEFAULT_SPEED` | `1.1` | Tốc độ mặc định (server atempo) |
| `MAX_SPEED` | `3.0` | Tốc độ tối đa khi slot chật |
| `PREFETCH_SEC` | `10` | Fetch TTS trước bao nhiêu giây |
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
| `yt-narration-on` | Trạng thái nút thuyết minh |
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

File config: `services/nllb_server.py` (dòng `MODEL_NAME`). Port 8005.

### NLLB-200-distilled-600M (đang dùng)

| Ưu | Nhược |
|-----|-------|
| Dịch 200 ngôn ngữ, EN→VI khá tốt | Nặng ~2.5GB disk, ~4GB RAM |
| Ngữ cảnh ổn với câu dài | Chậm (~2-3s/câu lần đầu, ~0.5s sau warmup) |
| Phân biệt được ngữ cảnh formal/casual | `you` → `anh` (phải dùng pronoun replacement) |

### Helsinki-NLP/opus-mt-en-vi

| Ưu | Nhược |
|-----|-------|
| Nhẹ ~300MB disk/RAM | Chỉ EN→VI, ko hỗ trợ language khác |
| Nhanh hơn NLLB (~0.3s/câu) | Chất lượng thấp hơn, dịch thô |
| Chuyên cho EN→VI | Dịch `Dr.` → `Tiến sĩ` (sai context) |

### Cách đổi model

1. Sửa `MODEL_NAME` trong `services/nllb_server.py`
2. Kill port 8005, xoá cache model cũ: `rm -rf ~/.cache/huggingface/hub/models--facebook--nllb*`
3. Restart: `/tmp/nllb-venv/bin/python services/nllb_server.py &`
4. Model mới tự tải từ HuggingFace lần đầu chạy

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
| 8005 | NLLB Translate | `/tmp/nllb-venv/bin/python services/nllb_server.py` |
| 5173 | Vite (web) | `scripts/dev.sh` (auto) |
