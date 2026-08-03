# Narration Translation Engines Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cache narration translations on disk and add a second translation engine (Qwen3-8B via MLX) alongside the existing NLLB per-cue path, with a player menu that selects engine and output mode so the two can be compared before one is retired.

**Architecture:** The translate sidecar on port 8005 grows a batch endpoint and a second engine; it owns line-count validation and per-cue fallback because only it knows how many cues it was handed. The gateway gains two routes that read and write one JSON file per video under `MEDIA_ROOT`, keyed by `sha1(en_text)` and partitioned by engine so the two engines never blend. The browser translates ahead of the playhead in ramped batches instead of one cue at a time, filling an in-memory map that is seeded from and flushed to that file.

**Tech Stack:** Python 3 + FastAPI + `mlx-lm` (sidecar), Go 1.x `net/http` (gateway), React + TypeScript + vitest (web).

## Global Constraints

- All source code, identifiers, comments and commit messages MUST be in English (CLAUDE.md §4b).
- **Known conflict — resolve before Task 6:** §4b also requires English UI copy, but `Player.tsx` already ships Vietnamese UI strings (`"Phụ đề"`, `"Thuyết minh"`, `"Tắt"`, `"Tự động phát"`). This plan matches the surrounding file (Vietnamese) so the settings menu is not half-translated. If the charter is to be honoured literally, that is a separate sweep across the whole file, not something this plan should do silently.
- No `fetch` inside `ui/` — repository calls live in `infrastructure/` or `application/` (CLAUDE.md §5).
- Frontend tests run with `cd web && npx vitest run <path>`.
- Go builds with `cd services/gateway && go build ./...`.
- Python sidecar runs from `/tmp/nllb-venv/bin/python`.
- Engine ids are exactly `"nllb"` and `"qwen"`. Output modes are exactly `"off"`, `"subs"`, `"voice"`, `"both"`.
- Batch sizes: first batch **3** cues, subsequent batches **15** cues, **3** preceding cues sent as untranslated context.
- Qwen model id: `mlx-community/Qwen3-8B-4bit` (already in `~/.cache/huggingface`).
- Never let a translation failure stop playback — every failure path degrades to untranslated or to NLLB, never throws to the player.

---

### Task 1: Narration cache file format and gateway routes

The cache is one JSON file per video, partitioned by engine. Partitioning is not a nicety: both engines stay live for A/B comparison, and a single `sha1 → text` map would silently overwrite one engine's output with the other's, making the comparison meaningless.

Verified safe against eviction: `services/catalog/internal/usecase/evict.go:90` calls `os.Remove(path)` on the single media file, never `RemoveAll` on the directory, so this file survives an evicted video exactly as `*.en.vtt` does.

**Files:**
- Create: `services/gateway/internal/api/narration_cache.go`
- Create: `services/gateway/internal/api/narration_cache_test.go`
- Modify: `services/gateway/internal/api/router.go:73` (add two routes after the existing translate route)
- Modify: `services/gateway/internal/api/gateway.go` (add `mediaRoot` field — check the actual struct file name first with `grep -rn "type Gateway struct" services/gateway/internal/api/`)
- Modify: `services/gateway/cmd/gateway/main.go:61` (pass `mediaRoot` into the Gateway)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `GET /api/videos/{id}/narration-cache?engine=<id>` → `200 {"entries": {"<sha1>": "<vi text>"}}`. Missing file, unreadable file, or unknown engine all return `200` with an empty object — a cold cache is the normal case, not an error.
  - `POST /api/videos/{id}/narration-cache` with body `{"engine": "qwen", "entries": {"<sha1>": "<vi>"}}` → `200 {"written": <int>}`. Merges into the existing file; never replaces other engines' partitions.
  - On-disk shape at `{MEDIA_ROOT}/{videoId}/narration.vi.json`:
    ```json
    { "nllb": { "<sha1>": "..." }, "qwen": { "<sha1>": "..." } }
    ```

- [ ] **Step 1: Write the failing test**

Create `services/gateway/internal/api/narration_cache_test.go`:

```go
package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNarrationCacheRoundTrip(t *testing.T) {
	root := t.TempDir()

	if err := writeNarrationCache(root, "abc123", "qwen",
		map[string]string{"h1": "xin chào"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A second engine must not disturb the first.
	if err := writeNarrationCache(root, "abc123", "nllb",
		map[string]string{"h1": "chào bạn"}); err != nil {
		t.Fatalf("write nllb: %v", err)
	}

	got, err := readNarrationCache(root, "abc123", "qwen")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got["h1"] != "xin chào" {
		t.Fatalf("qwen partition clobbered: %q", got["h1"])
	}

	nllb, _ := readNarrationCache(root, "abc123", "nllb")
	if nllb["h1"] != "chào bạn" {
		t.Fatalf("nllb partition wrong: %q", nllb["h1"])
	}

	raw, _ := os.ReadFile(filepath.Join(root, "abc123", "narration.vi.json"))
	var onDisk map[string]map[string]string
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("file is not the documented shape: %v", err)
	}
	if len(onDisk) != 2 {
		t.Fatalf("want 2 engine partitions, got %d", len(onDisk))
	}
}

func TestNarrationCacheMissingIsEmptyNotError(t *testing.T) {
	got, err := readNarrationCache(t.TempDir(), "nope", "qwen")
	if err != nil {
		t.Fatalf("a cold cache must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %d", len(got))
	}
}

func TestNarrationCacheRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	err := writeNarrationCache(root, "../../etc", "qwen",
		map[string]string{"h": "x"})
	if err == nil {
		t.Fatal("video id traversal must be rejected")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/gateway && go test ./internal/api/ -run TestNarrationCache -v`
Expected: FAIL — `undefined: writeNarrationCache`, `undefined: readNarrationCache`.

- [ ] **Step 3: Write minimal implementation**

Create `services/gateway/internal/api/narration_cache.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// narrationCacheFile is the per-video store of machine translations, laid down
// beside the media it belongs to. Partitioned by engine because both engines
// stay live for comparison, and one shared key space would let the second
// engine overwrite the first's answer for the same sentence.
const narrationCacheFile = "narration.vi.json"

var errBadVideoID = errors.New("bad video id")

// safeVideoDir rejects anything that could climb out of MEDIA_ROOT. Video ids
// arrive from the URL path, so they are untrusted input.
func safeVideoDir(root, videoID string) (string, error) {
	if videoID == "" || strings.ContainsAny(videoID, `/\`) || strings.Contains(videoID, "..") {
		return "", errBadVideoID
	}
	return filepath.Join(root, videoID), nil
}

func readNarrationCache(root, videoID, engine string) (map[string]string, error) {
	dir, err := safeVideoDir(root, videoID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, narrationCacheFile))
	if err != nil {
		// A cold cache is the ordinary state of a video nobody has narrated.
		return map[string]string{}, nil
	}
	var all map[string]map[string]string
	if err := json.Unmarshal(raw, &all); err != nil {
		// A corrupt file is worth re-translating over, not worth failing on.
		return map[string]string{}, nil
	}
	if part, ok := all[engine]; ok {
		return part, nil
	}
	return map[string]string{}, nil
}

func writeNarrationCache(root, videoID, engine string, entries map[string]string) error {
	dir, err := safeVideoDir(root, videoID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, narrationCacheFile)

	all := map[string]map[string]string{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &all)
	}
	if all[engine] == nil {
		all[engine] = map[string]string{}
	}
	for k, v := range entries {
		all[engine][k] = v
	}

	blob, err := json.Marshal(all)
	if err != nil {
		return err
	}
	// Write-then-rename so a reader never sees half a file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (g *Gateway) handleGetNarrationCache(w http.ResponseWriter, r *http.Request) {
	engine := r.URL.Query().Get("engine")
	entries, err := readNarrationCache(g.mediaRoot, r.PathValue("id"), engine)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
}

func (g *Gateway) handlePutNarrationCache(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Engine  string            `json:"engine"`
		Entries map[string]string `json:"entries"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if body.Engine == "" || len(body.Entries) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"written": 0})
		return
	}
	if err := writeNarrationCache(g.mediaRoot, r.PathValue("id"), body.Engine, body.Entries); err != nil {
		// MEDIA_ROOT can be an unmounted external SSD (CLAUDE.md §8.1). Losing
		// the cache is survivable; failing the request is not worth it.
		g.logger.Warn("narration cache write", "error", err)
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"written": len(body.Entries)})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services/gateway && go test ./internal/api/ -run TestNarrationCache -v`
Expected: PASS — all three tests.

- [ ] **Step 5: Add the `mediaRoot` field and wire the routes**

Find the Gateway struct: `grep -rn "type Gateway struct" services/gateway/internal/api/`. Add a field `mediaRoot string` and set it wherever the struct is constructed. In `services/gateway/cmd/gateway/main.go`, `mediaRoot` already exists at line 61 — pass it through to the Gateway constructor.

In `services/gateway/internal/api/router.go`, directly after line 73:

```go
	mux.HandleFunc("GET /api/videos/{id}/narration-cache", g.handleGetNarrationCache)
	mux.HandleFunc("POST /api/videos/{id}/narration-cache", g.handlePutNarrationCache)
```

- [ ] **Step 6: Verify it builds and the whole package still passes**

Run: `cd services/gateway && go build ./... && go test ./internal/api/`
Expected: build succeeds, tests PASS.

- [ ] **Step 7: Commit**

```bash
git add services/gateway/internal/api/narration_cache.go \
        services/gateway/internal/api/narration_cache_test.go \
        services/gateway/internal/api/router.go \
        services/gateway/internal/api/gateway.go \
        services/gateway/cmd/gateway/main.go
git commit -m "feat: store narration translations beside the video, per engine"
```

---

### Task 2: Batch translation endpoint with a second engine

The sidecar owns alignment checking. Measured on real cues: fed fragmented input, Qwen returned 9 of 12 numbered lines and Gemma 44 of 60 — every later line silently shifted onto the wrong cue. Fed cues grouped by the real parser both scored 60/60, but "usually correct" is not a contract. The count check is mandatory, and the per-cue retry belongs here because only the server can retry without a round trip.

**Files:**
- Create: `services/translate_server.py`
- Create: `services/test_translate_server.py`
- Delete: `services/nllb_server.py` (its behaviour moves wholesale into the new file)
- Modify: `scripts/dev.sh:66` (start the renamed file)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `POST /translate` — unchanged from today: `{"text","src","tgt"}` → `{"translated"}`. Still NLLB. The player's realtime path depends on this and must not change.
  - `POST /translate/batch` → request `{"engine": "qwen"|"nllb", "cues": ["..."], "context": ["..."]}`, response `{"translations": ["...", ...], "engine": "...", "fell_back": bool}`. `translations` always has exactly `len(cues)` entries; an entry that could not be translated is the empty string.
  - `GET /health` → `{"status","engines":{"nllb":bool,"qwen":bool}}`.

- [ ] **Step 1: Write the failing test**

Create `services/test_translate_server.py`. These test the pure parsing/alignment logic, which is where the risk is — not the model, which cannot be unit tested cheaply.

```python
"""Alignment tests for the batch translation endpoint.

The model is not exercised here. What is exercised is the part that decides
whether the model's answer may be trusted, which is the part that failed in
measurement.
"""
from translate_server import parse_numbered, aligned_or_none


def test_parse_numbered_reads_dot_and_paren():
    out = parse_numbered("1. một\n2) hai\n3. ba")
    assert out == {1: "một", 2: "hai", 3: "ba"}


def test_parse_numbered_ignores_preamble():
    # Gemma was measured emitting a lead-in outside the requested format.
    out = parse_numbered("Bản dịch:\n1. một\n2. hai")
    assert out == {1: "một", 2: "hai"}


def test_parse_numbered_strips_think_block():
    out = parse_numbered("<think>hmm</think>\n1. một")
    assert out == {1: "một"}


def test_aligned_returns_list_when_every_line_present():
    assert aligned_or_none({1: "a", 2: "b"}, 2) == ["a", "b"]


def test_aligned_returns_none_on_short_answer():
    # The measured failure: 15 sent, fewer returned, rest silently shifted.
    assert aligned_or_none({1: "a", 2: "b"}, 3) is None


def test_aligned_returns_none_on_gap():
    assert aligned_or_none({1: "a", 3: "c"}, 3) is None
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services && /tmp/nllb-venv/bin/python -m pytest test_translate_server.py -v`
Expected: FAIL — `ModuleNotFoundError: No module named 'translate_server'`.
(If pytest is absent: `/tmp/nllb-venv/bin/pip install pytest`.)

- [ ] **Step 3: Write minimal implementation**

Create `services/translate_server.py`. This is `nllb_server.py` with its behaviour preserved verbatim, plus a batch path and a second engine.

```python
"""EN -> VI translation for narration. Default port 8005.

Two engines live side by side so their output can be compared before one is
retired:

  nllb  facebook/nllb-200-distilled-600M on MPS, one sentence per call. Fast to
        start, no context between cues, and it renders "you" as "anh" unless the
        caller substitutes markers first (see narration-translate.ts).
  qwen  mlx-community/Qwen3-8B-4bit through mlx-lm. Translates a batch with the
        preceding cues as context and obeys an instruction to address the viewer
        as "bạn", so the marker hack is unnecessary on this path.

Measured on an M4: NLLB ~10 words/s, Qwen ~7.4 words/s. Qwen is not faster —
it is chosen for quality, and the speed comes from caching results on disk.
"""
import os
import re
import time

import uvicorn
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

NLLB_MODEL = os.environ.get("NLLB_MODEL", "facebook/nllb-200-distilled-600M")
QWEN_MODEL = os.environ.get("QWEN_MODEL", "mlx-community/Qwen3-8B-4bit")
PORT = int(os.environ.get("NLLB_PORT", "8005"))

BATCH_PROMPT = """Dịch các câu phụ đề tiếng Anh sau sang tiếng Việt.

Quy tắc:
- Xưng hô với người xem là "bạn", không dùng "anh"/"chị"/"quý vị".
- Văn nói tự nhiên, không dịch từng chữ.
- Dịch đúng nội dung câu gốc. KHÔNG thêm ý, KHÔNG bình luận.
- Giữ nguyên thuật ngữ tiếng Anh đã quen dùng, nhưng dịch từ thông thường.
- Trả về ĐÚNG {n} dòng, mỗi dòng dạng "số. bản dịch". Không giải thích.
{ctx}
Cần dịch:
{body}"""


# ---- answer parsing ---------------------------------------------------------

def parse_numbered(text: str) -> dict[int, str]:
    """Pull "<n>. <text>" lines out of a model answer.

    Tolerant on purpose: models were measured adding a lead-in ("Bản dịch:") and
    reasoning models emit a <think> block. Neither is worth failing over — what
    matters is whether every requested number came back, which aligned_or_none
    decides.
    """
    text = re.sub(r"<think>.*?</think>", "", text, flags=re.S)
    out: dict[int, str] = {}
    for num, body in re.findall(r"^\s*(\d+)[.)]\s*(.+)$", text, flags=re.M):
        out[int(num)] = body.strip()
    return out


def aligned_or_none(parsed: dict[int, str], want: int) -> list[str] | None:
    """Return the answers in order, or None if the batch cannot be trusted.

    A batch that is short by even one line is not partially usable: the caller
    maps answers onto cues by position, so a gap silently speaks every later
    line at the wrong moment. Refusing the whole batch is the only safe read.
    """
    if len(parsed) != want:
        return None
    if any(i not in parsed for i in range(1, want + 1)):
        return None
    return [parsed[i] for i in range(1, want + 1)]


# ---- engines ----------------------------------------------------------------

_nllb = None
_qwen = None


def nllb():
    global _nllb
    if _nllb is None:
        import torch
        from transformers import AutoModelForSeq2SeqLM, AutoTokenizer
        device = "mps" if torch.backends.mps.is_available() else "cpu"
        tok = AutoTokenizer.from_pretrained(NLLB_MODEL)
        mod = AutoModelForSeq2SeqLM.from_pretrained(NLLB_MODEL).to(device)
        _nllb = (tok, mod, device, torch)
    return _nllb


def qwen():
    global _qwen
    if _qwen is None:
        from mlx_lm import load
        _qwen = load(QWEN_MODEL)
    return _qwen


def nllb_one(text: str, src: str, tgt: str) -> str:
    tok, mod, device, torch = nllb()
    tok.src_lang = src
    inputs = tok(text, return_tensors="pt").to(device)
    with torch.no_grad():
        out = mod.generate(
            **inputs,
            forced_bos_token_id=tok.convert_tokens_to_ids(tgt),
            max_new_tokens=min(256, max(32, int(len(text.split()) * 3))),
            num_beams=4,
        )
    return tok.batch_decode(out, skip_special_tokens=True)[0]


def qwen_batch(cues: list[str], context: list[str]) -> list[str] | None:
    from mlx_lm import generate
    model, tok = qwen()
    ctx = ""
    if context:
        ctx = "\nNgữ cảnh (các câu ngay trước, KHÔNG dịch):\n" + \
              "\n".join(f"- {c}" for c in context) + "\n"
    body = "\n".join(f"{i + 1}. {c}" for i, c in enumerate(cues))
    msg = [{"role": "user",
            "content": BATCH_PROMPT.format(n=len(cues), ctx=ctx, body=body)}]
    try:
        prompt = tok.apply_chat_template(
            msg, add_generation_prompt=True, enable_thinking=False)
    except TypeError:
        prompt = tok.apply_chat_template(msg, add_generation_prompt=True)
    answer = generate(model, tok, prompt=prompt, max_tokens=1600, verbose=False)
    return aligned_or_none(parse_numbered(answer), len(cues))


# ---- app --------------------------------------------------------------------

app = FastAPI()
app.add_middleware(CORSMiddleware, allow_origins=["*"],
                   allow_methods=["*"], allow_headers=["*"])


@app.get("/health")
def health():
    return {"status": "ok",
            "engines": {"nllb": _nllb is not None, "qwen": _qwen is not None}}


@app.post("/translate")
async def translate(req: Request):
    body = await req.json()
    text = (body.get("text") or "").strip()
    if not text:
        return JSONResponse({"translated": ""})
    t0 = time.perf_counter()
    out = nllb_one(text, body.get("src", "eng_Latn"), body.get("tgt", "vie_Latn"))
    print(f"[{time.perf_counter() - t0:.2f}s] {text[:50]} -> {out[:50]}")
    return JSONResponse({"translated": out})


@app.post("/translate/batch")
async def translate_batch(req: Request):
    body = await req.json()
    cues = [c for c in (body.get("cues") or [])]
    if not cues:
        return JSONResponse({"translations": [], "engine": "", "fell_back": False})
    engine = body.get("engine", "qwen")
    context = body.get("context") or []

    t0 = time.perf_counter()
    fell_back = False

    if engine == "qwen":
        out = qwen_batch(cues, context)
        if out is None:
            # The batch could not be trusted. Retry one cue at a time, where
            # there is no ordering left to get wrong.
            fell_back = True
            out = []
            for c in cues:
                single = qwen_batch([c], [])
                out.append(single[0] if single else "")
    else:
        out = [nllb_one(c, "eng_Latn", "vie_Latn") for c in cues]

    dt = time.perf_counter() - t0
    words = sum(len(c.split()) for c in cues)
    print(f"[batch {engine}] {len(cues)} cues / {words} words in {dt:.1f}s"
          f"{' FELL BACK' if fell_back else ''}")
    return JSONResponse({"translations": out, "engine": engine,
                         "fell_back": fell_back})


if __name__ == "__main__":
    print(f"translate server on {PORT}; engines load on first use")
    uvicorn.run(app, host="0.0.0.0", port=PORT, access_log=False)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd services && /tmp/nllb-venv/bin/python -m pytest test_translate_server.py -v`
Expected: PASS — 6 tests.

- [ ] **Step 5: Verify the live endpoints by hand**

Kill whatever holds 8005 (`lsof -nP -iTCP:8005 -sTCP:LISTEN`), then:

```bash
/tmp/nllb-venv/bin/python services/translate_server.py &
sleep 3
curl -s -X POST localhost:8005/translate \
  -H 'Content-Type: application/json' \
  -d '{"text":"Hello there","src":"eng_Latn","tgt":"vie_Latn"}'
curl -s -X POST localhost:8005/translate/batch \
  -H 'Content-Type: application/json' \
  -d '{"engine":"qwen","cues":["Hello there.","How are you today?"],"context":[]}'
```

Expected: the first returns `{"translated": "..."}`. The second returns exactly 2 translations, `"fell_back": false`, and both address the viewer as `bạn` where a pronoun occurs. First Qwen call includes a ~4s model load.

- [ ] **Step 6: Point dev.sh at the renamed file**

In `scripts/dev.sh`, replace `services/nllb_server.py` with `services/translate_server.py` on both lines 64 and 66, and update the comment on line 7 to say "translate 8005".

- [ ] **Step 7: Commit**

```bash
git rm services/nllb_server.py
git add services/translate_server.py services/test_translate_server.py scripts/dev.sh
git commit -m "feat: add batched Qwen translation beside NLLB, refusing misaligned answers"
```

---

### Task 3: Browser-side cache repository

**Files:**
- Create: `web/src/features/watch/infrastructure/narration-cache.ts`
- Create: `web/src/features/watch/infrastructure/narration-cache.test.ts`

**Interfaces:**
- Consumes: the two gateway routes from Task 1.
- Produces:
  - `export type NarrationEngine = 'nllb' | 'qwen'`
  - `export async function hashCue(text: string): Promise<string>` — hex sha1 of the exact cue text.
  - `export async function loadNarrationCache(videoId: string, engine: NarrationEngine): Promise<Map<string, string>>` — never rejects; a failure is an empty map.
  - `export async function saveNarrationCache(videoId: string, engine: NarrationEngine, entries: Map<string, string>): Promise<void>` — never rejects.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/watch/infrastructure/narration-cache.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  hashCue,
  loadNarrationCache,
  saveNarrationCache,
} from './narration-cache'

afterEach(() => vi.unstubAllGlobals())

describe('hashCue', () => {
  it('is stable and differs on different text', async () => {
    const a = await hashCue('Hello there.')
    const b = await hashCue('Hello there.')
    const c = await hashCue('Hello there!')
    expect(a).toBe(b)
    expect(a).not.toBe(c)
    expect(a).toMatch(/^[0-9a-f]{40}$/)
  })
})

describe('loadNarrationCache', () => {
  it('returns the entries as a map', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ entries: { h1: 'xin chào' } }),
    }))
    const got = await loadNarrationCache('vid', 'qwen')
    expect(got.get('h1')).toBe('xin chào')
  })

  it('returns an empty map when the request fails', async () => {
    // MEDIA_ROOT can be an unmounted SSD. Narration must still work.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')))
    const got = await loadNarrationCache('vid', 'qwen')
    expect(got.size).toBe(0)
  })
})

describe('saveNarrationCache', () => {
  it('posts the engine and entries', async () => {
    const f = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) })
    vi.stubGlobal('fetch', f)
    await saveNarrationCache('vid', 'nllb', new Map([['h1', 'chào']]))
    const [url, init] = f.mock.calls[0]
    expect(url).toBe('/api/videos/vid/narration-cache')
    expect(JSON.parse(init.body)).toEqual({
      engine: 'nllb',
      entries: { h1: 'chào' },
    })
  })

  it('does not throw when the write fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('no disk')))
    await expect(
      saveNarrationCache('vid', 'qwen', new Map([['h', 'x']])),
    ).resolves.toBeUndefined()
  })

  it('skips the request entirely when there is nothing to write', async () => {
    const f = vi.fn()
    vi.stubGlobal('fetch', f)
    await saveNarrationCache('vid', 'qwen', new Map())
    expect(f).not.toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/features/watch/infrastructure/narration-cache.test.ts`
Expected: FAIL — cannot resolve `./narration-cache`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/features/watch/infrastructure/narration-cache.ts`:

```ts
/**
 * Translations, kept beside the video they belong to.
 *
 * A cue's translation is a pure function of its text and never changes, so
 * paying for it once per browser session — which is what an in-memory map
 * amounted to — was paying for the same sentence forever. The store lives on
 * the server so a reload, a second viewer, and the TV all read the same answer.
 *
 * Keyed by the hash of the cue text rather than by position, because the cue
 * grouping in narration-vtt.ts has been retuned a dozen times; a positional key
 * would throw the whole cache away on the next tweak, a content key loses only
 * the cues that actually changed.
 */

export type NarrationEngine = 'nllb' | 'qwen'

/** Hex SHA-1 of a cue's text. */
export async function hashCue(text: string): Promise<string> {
  const bytes = new TextEncoder().encode(text)
  const digest = await crypto.subtle.digest('SHA-1', bytes)
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}

export async function loadNarrationCache(
  videoId: string,
  engine: NarrationEngine,
): Promise<Map<string, string>> {
  try {
    const resp = await fetch(
      `/api/videos/${videoId}/narration-cache?engine=${engine}`,
    )
    if (!resp.ok) return new Map()
    const body = (await resp.json()) as { entries?: Record<string, string> }
    return new Map(Object.entries(body.entries ?? {}))
  } catch {
    // The store is an optimisation. Losing it costs time, not correctness.
    return new Map()
  }
}

export async function saveNarrationCache(
  videoId: string,
  engine: NarrationEngine,
  entries: Map<string, string>,
): Promise<void> {
  if (entries.size === 0) return
  try {
    await fetch(`/api/videos/${videoId}/narration-cache`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ engine, entries: Object.fromEntries(entries) }),
    })
  } catch {
    // Same reason as above.
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/features/watch/infrastructure/narration-cache.test.ts`
Expected: PASS — 6 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/watch/infrastructure/narration-cache.ts \
        web/src/features/watch/infrastructure/narration-cache.test.ts
git commit -m "feat: read and write narration translations through the gateway"
```

---

### Task 4: Batch translation client with ramped batch sizes

The first batch is deliberately small. A batch of 15 was measured at roughly 20 seconds; making the viewer wait that long before the first spoken word would trade one complaint for a worse one. Three cues gets a voice going, and once playback is under way the background pass runs 2.4–3.7× faster than speech and never needs the head start again.

**Files:**
- Create: `web/src/features/watch/application/narration-batch.ts`
- Create: `web/src/features/watch/application/narration-batch.test.ts`

**Interfaces:**
- Consumes: `POST /api/translate/batch` (added to the gateway in Task 5), `NarrationEngine` from Task 3.
- Produces:
  - `export const FIRST_BATCH = 3`
  - `export const BATCH_SIZE = 15`
  - `export const CONTEXT_CUES = 3`
  - `export function planBatches(total: number): Array<{ start: number; end: number }>` — index ranges, first one short.
  - `export async function translateBatch(cues: string[], context: string[], engine: NarrationEngine): Promise<string[]>` — always resolves to exactly `cues.length` strings; empty string where translation failed.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/watch/application/narration-batch.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from 'vitest'
import { BATCH_SIZE, FIRST_BATCH, planBatches, translateBatch } from './narration-batch'

afterEach(() => vi.unstubAllGlobals())

describe('planBatches', () => {
  it('keeps the first batch short so the first word arrives quickly', () => {
    const plan = planBatches(40)
    expect(plan[0]).toEqual({ start: 0, end: FIRST_BATCH })
    expect(plan[1]).toEqual({ start: FIRST_BATCH, end: FIRST_BATCH + BATCH_SIZE })
  })

  it('covers every cue exactly once with no gap', () => {
    const plan = planBatches(40)
    expect(plan[0].start).toBe(0)
    expect(plan[plan.length - 1].end).toBe(40)
    for (let i = 1; i < plan.length; i++) {
      expect(plan[i].start).toBe(plan[i - 1].end)
    }
  })

  it('handles a video shorter than one batch', () => {
    expect(planBatches(2)).toEqual([{ start: 0, end: 2 }])
  })

  it('returns nothing for no cues', () => {
    expect(planBatches(0)).toEqual([])
  })
})

describe('translateBatch', () => {
  it('returns the server translations', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ translations: ['một', 'hai'] }),
    }))
    expect(await translateBatch(['one', 'two'], [], 'qwen')).toEqual(['một', 'hai'])
  })

  it('pads a short answer rather than shifting cues', async () => {
    // Belt and braces: the server refuses misaligned batches, but a length
    // mismatch here would speak every later cue at the wrong moment.
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ translations: ['một'] }),
    }))
    expect(await translateBatch(['one', 'two'], [], 'qwen')).toEqual(['một', ''])
  })

  it('resolves to blanks when the request fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('down')))
    expect(await translateBatch(['one', 'two'], [], 'qwen')).toEqual(['', ''])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/features/watch/application/narration-batch.test.ts`
Expected: FAIL — cannot resolve `./narration-batch`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/features/watch/application/narration-batch.ts`:

```ts
import type { NarrationEngine } from '@/features/watch/infrastructure/narration-cache'

/**
 * Cues in the opening batch.
 *
 * Small on purpose. A full batch of fifteen was measured at about twenty
 * seconds, and twenty seconds of silence after switching narration on reads as
 * broken. Three cues is enough to start talking; by the time they are spoken
 * the background pass is already well ahead, because translation outruns speech
 * by between two and four times.
 */
export const FIRST_BATCH = 3

/** Cues per batch once playback is under way. */
export const BATCH_SIZE = 15

/** Preceding cues sent along for context, not for translation. */
export const CONTEXT_CUES = 3

export function planBatches(total: number): Array<{ start: number; end: number }> {
  const out: Array<{ start: number; end: number }> = []
  let i = 0
  while (i < total) {
    const size = out.length === 0 ? FIRST_BATCH : BATCH_SIZE
    out.push({ start: i, end: Math.min(i + size, total) })
    i += size
  }
  return out
}

export async function translateBatch(
  cues: string[],
  context: string[],
  engine: NarrationEngine,
): Promise<string[]> {
  const blank = cues.map(() => '')
  if (cues.length === 0) return []
  try {
    const resp = await fetch('/api/translate/batch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ engine, cues, context }),
    })
    if (!resp.ok) return blank
    const body = (await resp.json()) as { translations?: string[] }
    const got = body.translations ?? []
    // Length is load-bearing: the caller maps these onto cues by position, so a
    // short answer must be padded rather than allowed to shift everything after
    // it onto the wrong cue.
    return cues.map((_, i) => got[i] ?? '')
  } catch {
    return blank
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/features/watch/application/narration-batch.test.ts`
Expected: PASS — 7 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/watch/application/narration-batch.ts \
        web/src/features/watch/application/narration-batch.test.ts
git commit -m "feat: plan translation batches with a short opening batch"
```

---

### Task 5: Gateway route for batch translation

**Files:**
- Modify: `services/gateway/internal/api/translate.go` (add a handler beside `handleTranslate`)
- Modify: `services/gateway/internal/api/router.go:73` (add one route)

**Interfaces:**
- Consumes: `POST /translate/batch` on the sidecar (Task 2).
- Produces: `POST /api/translate/batch` — request and response bodies pass through unchanged, so Task 4's client contract holds.

- [ ] **Step 1: Add the handler**

Append to `services/gateway/internal/api/translate.go`:

```go
// handleTranslateBatch forwards a whole batch to the sidecar untouched.
//
// Deliberately a pass-through: the decision about whether a batch came back
// correctly aligned belongs where the request was assembled, and that is the
// sidecar. Re-checking here would be a second opinion with less information.
func (g *Gateway) handleTranslateBatch(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"http://localhost:8005/translate/batch", bytes.NewReader(body))
	if err != nil {
		g.logger.Warn("translate batch build request", "error", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := g.streamClient.Do(proxyReq)
	if err != nil {
		g.logger.Warn("translate batch upstream", "error", err)
		http.Error(w, "translation service unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.logger.Warn("translate batch status", "status", resp.StatusCode)
		http.Error(w, "translation failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	n, _ := io.Copy(w, resp.Body)
	g.logger.Info("translate batch", "ms", time.Since(start).Milliseconds(), "bytes", n)
}
```

- [ ] **Step 2: Add the route**

In `services/gateway/internal/api/router.go`, directly after line 73:

```go
	mux.HandleFunc("POST /api/translate/batch", g.handleTranslateBatch)
```

- [ ] **Step 3: Verify it builds**

Run: `cd services/gateway && go build ./... && go vet ./internal/api/`
Expected: no output.

- [ ] **Step 4: Verify end to end against the running sidecar**

With `services/translate_server.py` and the gateway both running:

```bash
curl -s -X POST localhost:8180/api/translate/batch \
  -H 'Content-Type: application/json' \
  -d '{"engine":"qwen","cues":["Hello there.","Thanks for watching."],"context":[]}'
```

Expected: exactly 2 translations, `"fell_back": false`.

- [ ] **Step 5: Commit**

```bash
git add services/gateway/internal/api/translate.go services/gateway/internal/api/router.go
git commit -m "feat: expose batch translation through the gateway"
```

---

### Task 6: Translate ahead of the playhead instead of one cue at a time

The background pass starts at the playhead, not at cue zero. Resuming at minute thirty must not spend its first minutes translating the opening credits — and starting at the playhead is strictly better than today's two-cue lookahead in every case, including a cold resume.

**Files:**
- Modify: `web/src/features/watch/application/narration.ts` — `_tlCache` at line 65, `fetchTTS` at line 67, `loadViSubtitles` at line 234
- Create: `web/src/features/watch/application/narration-pass.test.ts`

**Interfaces:**
- Consumes: `planBatches`, `translateBatch`, `CONTEXT_CUES` (Task 4); `hashCue`, `loadNarrationCache`, `saveNarrationCache`, `NarrationEngine` (Task 3).
- Produces from `narration.ts`:
  - `export function setNarrationEngine(engine: NarrationEngine): void`
  - `export function startTranslationPass(videoId: string, fromTime: number): void` — begins or re-anchors the background pass. Safe to call repeatedly.
  - `export function translatedCue(text: string): string | undefined` — synchronous cache read used by both the TTS path and the subtitle overlay.
  - `export function nearestCueIndex(cues: CueText[], time: number): number`

- [ ] **Step 1: Write the failing test**

Create `web/src/features/watch/application/narration-pass.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { nearestCueIndex } from './narration'
import type { CueText } from './narration-vtt'

const cue = (start: number, end: number, text: string): CueText =>
  ({ start, end, text }) as CueText

describe('nearestCueIndex', () => {
  const cues = [
    cue(0, 2, 'a'),
    cue(10, 12, 'b'),
    cue(20, 22, 'c'),
    cue(30, 32, 'd'),
  ]

  it('starts the pass at the playhead, not at the beginning', () => {
    // Resuming at minute thirty must not translate the opening credits first.
    expect(nearestCueIndex(cues, 19)).toBe(2)
  })

  it('picks the cue in progress', () => {
    expect(nearestCueIndex(cues, 21)).toBe(2)
  })

  it('is zero at the start of the video', () => {
    expect(nearestCueIndex(cues, 0)).toBe(0)
  })

  it('clamps past the end rather than returning -1', () => {
    expect(nearestCueIndex(cues, 999)).toBe(3)
  })

  it('is zero when there are no cues', () => {
    expect(nearestCueIndex([], 5)).toBe(0)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/features/watch/application/narration-pass.test.ts`
Expected: FAIL — `nearestCueIndex` is not exported from `./narration`.

- [ ] **Step 3: Write minimal implementation**

In `web/src/features/watch/application/narration.ts`, add imports at the top beside the existing ones:

```ts
import {
  CONTEXT_CUES,
  planBatches,
  translateBatch,
} from './narration-batch'
import {
  hashCue,
  loadNarrationCache,
  saveNarrationCache,
  type NarrationEngine,
} from '@/features/watch/infrastructure/narration-cache'
```

Replace the `_tlCache` declaration at line 65 with the block below, and add the functions after it:

```ts
/** Cue text -> Vietnamese, for the engine currently selected. */
const _tlCache = new Map<string, string>()

let _engine: NarrationEngine = 'qwen'
let _passVideoId = ''
let _passRunning = false
let _passGeneration = 0
/** Written since the last flush, so a flush posts only what is new. */
const _unsaved = new Map<string, string>()

export function setNarrationEngine(engine: NarrationEngine) {
  if (engine === _engine) return
  // Each engine has its own answers; keeping the old ones would mix the two
  // and make the comparison this menu exists for meaningless.
  _engine = engine
  _tlCache.clear()
  _unsaved.clear()
  _passGeneration++
  _passRunning = false
}

export function translatedCue(text: string): string | undefined {
  return _tlCache.get(text)
}

/** Index of the cue playing at `time`, or the next one due. */
export function nearestCueIndex(cues: CueText[], time: number): number {
  if (cues.length === 0) return 0
  for (let i = 0; i < cues.length; i++) {
    if (cues[i].end > time) return i
  }
  return cues.length - 1
}

/**
 * Translate forward from the playhead until the video runs out.
 *
 * Not from cue zero: a viewer resuming at minute thirty would otherwise wait
 * while the opening is translated. Not one cue at a time either — a batch
 * carries the preceding lines as context, which is what keeps pronouns and
 * terminology steady across a video, and is the whole reason for using a model
 * that can read more than one sentence.
 *
 * Runs ahead of playback by roughly two to four times on this machine, so it
 * only has to keep going, not hurry.
 */
export function startTranslationPass(videoId: string, fromTime: number) {
  if (_passRunning && videoId === _passVideoId) return
  _passVideoId = videoId
  _passRunning = true
  _passGeneration++
  const generation = _passGeneration

  void (async () => {
    const disk = await loadNarrationCache(videoId, _engine)
    if (generation !== _passGeneration) return
    for (const [k, v] of disk) _tlCache.set(k, v)

    // Wait for the cue list, which loadViSubtitles is fetching in parallel.
    for (let i = 0; i < 100 && _cues === null; i++) {
      await new Promise((r) => setTimeout(r, 100))
    }
    const cues = _cues
    if (!cues || cues.length === 0 || generation !== _passGeneration) return

    const first = nearestCueIndex(cues, fromTime)
    const pending = cues.slice(first)
    const texts = pending.map((c) => c.text)
    const hashes = await Promise.all(texts.map(hashCue))

    for (const { start, end } of planBatches(texts.length)) {
      if (generation !== _passGeneration) return

      const slice = texts.slice(start, end)
      const missing = slice.filter((t) => !_tlCache.has(t))
      if (missing.length === 0) continue

      const context = texts.slice(Math.max(0, start - CONTEXT_CUES), start)
      const out = await translateBatch(missing, context, _engine)
      if (generation !== _passGeneration) return

      missing.forEach((text, i) => {
        const vi = out[i]
        if (!vi) return
        _tlCache.set(text, vi)
        const at = texts.indexOf(text)
        if (at >= 0) _unsaved.set(hashes[at], vi)
      })

      // Flush as we go. A viewer who closes the tab halfway keeps the half
      // that was paid for.
      const batchSaved = new Map(_unsaved)
      _unsaved.clear()
      void saveNarrationCache(videoId, _engine, batchSaved)
    }
    _passRunning = false
  })()
}
```

- [ ] **Step 4: Make `fetchTTS` read the cache instead of translating inline**

In `fetchTTS` (line 67), replace the whole `if (_sourceLang === 'en') { ... }` block with:

```ts
  // Translate EN -> VI when the source subtitle is English.
  let viText = text
  if (_sourceLang === 'en') {
    const cached = _tlCache.get(text)
    if (cached) {
      viText = cached
    } else if (_engine === 'nllb') {
      // The realtime engine still translates on demand: it is the arm of the
      // comparison that has no background pass, and taking that away would
      // leave nothing to compare against.
      const prepped = prepForTranslation(text)
      const resp = await fetch('/api/translate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: prepped, src: 'eng_Latn', tgt: 'vie_Latn' }),
      })
      if (resp.ok) {
        let translated = ((await resp.json()) as { translated: string }).translated
        if (translated) {
          translated = applyAfterTranslation(translated)
          _tlCache.set(text, translated)
          void hashCue(text).then((h) =>
            saveNarrationCache(_passVideoId, 'nllb', new Map([[h, translated]])),
          )
          viText = translated
        }
      }
    }
    // Under the batch engine an untranslated cue means the pass has not reached
    // it yet. Speaking the English is wrong, so the cue is skipped instead.
    if (viText === text && _engine === 'qwen') throw new Error('not translated yet')
  }
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd web && npx vitest run src/features/watch/application/narration-pass.test.ts`
Expected: PASS — 5 tests.

- [ ] **Step 6: Verify nothing else regressed**

Run: `cd web && npx vitest run src/features/watch/ && npx tsc --noEmit`
Expected: all existing narration tests still PASS, no type errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/watch/application/narration.ts \
        web/src/features/watch/application/narration-pass.test.ts
git commit -m "feat: translate ahead from the playhead in batches, backed by the disk cache"
```

---

### Task 7: Two-axis narration menu in the player

Two axes, kept visibly separate. Folding engine choice and output choice into one list of presets would make it impossible to tell whether a bad impression came from the engine or from the presentation — which is the same confounding that invalidated the first round of engine measurements.

**Files:**
- Modify: `web/src/features/watch/ui/Player.tsx` — state at line 422, gear-menu rows at line 1859, inline toggle at line 1809
- Create: `web/src/features/watch/application/narration-prefs.ts`
- Create: `web/src/features/watch/application/narration-prefs.test.ts`

**Interfaces:**
- Consumes: `NarrationEngine` (Task 3), `setNarrationEngine` (Task 6).
- Produces:
  - `export type NarrationOutput = 'off' | 'subs' | 'voice' | 'both'`
  - `export function loadNarrationPrefs(): { engine: NarrationEngine; output: NarrationOutput }`
  - `export function saveNarrationPrefs(p: { engine: NarrationEngine; output: NarrationOutput }): void`
  - Storage keys `yt-narration-engine-v1` and `yt-narration-output-v1`. The old boolean `yt-narration-on` is migrated once: `'1'` becomes `output: 'voice'`.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/watch/application/narration-prefs.test.ts`:

```ts
import { beforeEach, describe, expect, it } from 'vitest'
import { loadNarrationPrefs, saveNarrationPrefs } from './narration-prefs'

beforeEach(() => window.localStorage.clear())

describe('loadNarrationPrefs', () => {
  it('defaults to off with the batch engine', () => {
    expect(loadNarrationPrefs()).toEqual({ engine: 'qwen', output: 'off' })
  })

  it('migrates the old on/off switch to spoken output', () => {
    window.localStorage.setItem('yt-narration-on', '1')
    expect(loadNarrationPrefs().output).toBe('voice')
  })

  it('does not turn narration on for someone who had it off', () => {
    window.localStorage.setItem('yt-narration-on', '0')
    expect(loadNarrationPrefs().output).toBe('off')
  })

  it('ignores values that are not valid choices', () => {
    window.localStorage.setItem('yt-narration-engine-v1', 'gemma')
    window.localStorage.setItem('yt-narration-output-v1', 'shout')
    expect(loadNarrationPrefs()).toEqual({ engine: 'qwen', output: 'off' })
  })

  it('round-trips what was saved', () => {
    saveNarrationPrefs({ engine: 'nllb', output: 'both' })
    expect(loadNarrationPrefs()).toEqual({ engine: 'nllb', output: 'both' })
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/features/watch/application/narration-prefs.test.ts`
Expected: FAIL — cannot resolve `./narration-prefs`.

- [ ] **Step 3: Write minimal implementation**

Create `web/src/features/watch/application/narration-prefs.ts`:

```ts
import type { NarrationEngine } from '@/features/watch/infrastructure/narration-cache'

/**
 * What narration does with a translated cue: nothing, show it, speak it, or
 * both. Separate from which engine produced it, so the two can be judged one
 * at a time.
 */
export type NarrationOutput = 'off' | 'subs' | 'voice' | 'both'

const ENGINE_KEY = 'yt-narration-engine-v1'
const OUTPUT_KEY = 'yt-narration-output-v1'
const LEGACY_KEY = 'yt-narration-on'

const ENGINES: NarrationEngine[] = ['nllb', 'qwen']
const OUTPUTS: NarrationOutput[] = ['off', 'subs', 'voice', 'both']

export function loadNarrationPrefs(): {
  engine: NarrationEngine
  output: NarrationOutput
} {
  const rawEngine = window.localStorage.getItem(ENGINE_KEY)
  const rawOutput = window.localStorage.getItem(OUTPUT_KEY)

  const engine = ENGINES.includes(rawEngine as NarrationEngine)
    ? (rawEngine as NarrationEngine)
    : 'qwen'

  let output: NarrationOutput = 'off'
  if (OUTPUTS.includes(rawOutput as NarrationOutput)) {
    output = rawOutput as NarrationOutput
  } else if (window.localStorage.getItem(LEGACY_KEY) === '1') {
    // Someone who had the old switch on wanted a voice, not subtitles.
    output = 'voice'
  }
  return { engine, output }
}

export function saveNarrationPrefs(p: {
  engine: NarrationEngine
  output: NarrationOutput
}) {
  window.localStorage.setItem(ENGINE_KEY, p.engine)
  window.localStorage.setItem(OUTPUT_KEY, p.output)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/features/watch/application/narration-prefs.test.ts`
Expected: PASS — 5 tests.

- [ ] **Step 5: Replace the boolean state in Player.tsx**

At `Player.tsx:422`, replace the `narrationOn` state with prefs-backed state:

```tsx
  const [narrationPrefs, setNarrationPrefs] = useState(loadNarrationPrefs)
  const narrationOn = narrationPrefs.output !== 'off'
  const narrationSpeaks =
    narrationPrefs.output === 'voice' || narrationPrefs.output === 'both'
  const narrationShows =
    narrationPrefs.output === 'subs' || narrationPrefs.output === 'both'
```

Add a setter beside `toggleNarration` (line 951), keeping `toggleNarration` for the inline button at line 1809 — that button is a quick on/off and should stay one:

```tsx
  const setNarrationOutput = useCallback((output: NarrationOutput) => {
    setNarrationPrefs((p) => {
      const next = { ...p, output }
      saveNarrationPrefs(next)
      return next
    })
  }, [])

  const setEngine = useCallback((engine: NarrationEngine) => {
    setNarrationPrefs((p) => {
      const next = { ...p, engine }
      saveNarrationPrefs(next)
      return next
    })
    // Clears the cached answers of the engine being left behind.
    setNarrationEngine(engine)
  }, [])
```

Change `toggleNarration` to flip between `'off'` and `'voice'`:

```tsx
  const toggleNarration = useCallback(() => {
    if (!audioCtxRef.current) audioCtxRef.current = new AudioContext()
    void audioCtxRef.current.resume()
    setNarrationOutput(narrationOn ? 'off' : 'voice')
  }, [narrationOn, setNarrationOutput])
```

Gate the ducking and the tick loop on `narrationSpeaks` rather than `narrationOn`, at lines 581, 612 and 616 — showing subtitles must not duck the video's own audio.

- [ ] **Step 6: Replace the single settings row with the two-axis menu**

At `Player.tsx:1859`, replace the `narrationAvailable && <SettingRow label="Thuyết minh" …>` block with:

```tsx
                    {narrationAvailable && (
                      <>
                        <li className="px-4 pt-2 pb-1 text-xs text-text-2">
                          Thuyết minh
                        </li>
                        <SettingRow
                          label="Tắt"
                          on={narrationPrefs.output === 'off'}
                          onToggle={() => setNarrationOutput('off')}
                        />
                        <SettingRow
                          label="Chỉ phụ đề tiếng Việt"
                          on={narrationPrefs.output === 'subs'}
                          onToggle={() => setNarrationOutput('subs')}
                        />
                        <SettingRow
                          label="Chỉ giọng đọc"
                          on={narrationPrefs.output === 'voice'}
                          onToggle={() => setNarrationOutput('voice')}
                        />
                        <SettingRow
                          label="Cả hai"
                          on={narrationPrefs.output === 'both'}
                          onToggle={() => setNarrationOutput('both')}
                        />
                        <li className="px-4 pt-2 pb-1 text-xs text-text-2">
                          Máy dịch
                        </li>
                        <SettingRow
                          label="Qwen (dịch nền, có ngữ cảnh)"
                          on={narrationPrefs.engine === 'qwen'}
                          onToggle={() => setEngine('qwen')}
                        />
                        <SettingRow
                          label="NLLB (dịch ngay từng câu)"
                          on={narrationPrefs.engine === 'nllb'}
                          onToggle={() => setEngine('nllb')}
                        />
                      </>
                    )}
```

- [ ] **Step 7: Start the pass when narration turns on**

Extend the effect at line 625 that reacts to `narrationOn` so it also anchors the background pass at the current playhead:

```tsx
  useEffect(() => {
    if (!narrationOn) return
    if (narrationPrefs.engine !== 'qwen') return
    const el = videoRef.current
    startTranslationPass(videoId, el ? el.currentTime : 0)
  }, [narrationOn, narrationPrefs.engine, videoId, subtitles])
```

- [ ] **Step 8: Verify types and existing player tests**

Run: `cd web && npx tsc --noEmit && npx vitest run src/features/watch/`
Expected: no type errors; the existing `player-controls.test.tsx` and friends still PASS. If a test asserted on the old single "Thuyết minh" row, update that assertion to the new "Chỉ giọng đọc" row — the behaviour it covered still exists.

- [ ] **Step 9: Commit**

```bash
git add web/src/features/watch/application/narration-prefs.ts \
        web/src/features/watch/application/narration-prefs.test.ts \
        web/src/features/watch/ui/Player.tsx
git commit -m "feat: choose narration engine and output separately"
```

---

### Task 8: Show the translated cue on screen

The `subs` and `both` modes need somewhere to put the text. The translated cues are not a `<track>` — they exist only in memory — so they are drawn as an overlay rather than handed to the browser's caption renderer.

**Files:**
- Create: `web/src/features/watch/ui/NarrationSubtitles.tsx`
- Create: `web/src/features/watch/application/narration-current.test.ts`
- Modify: `web/src/features/watch/application/narration.ts` (export one helper)
- Modify: `web/src/features/watch/ui/Player.tsx` (render the overlay)

**Interfaces:**
- Consumes: `translatedCue`, `nearestCueIndex` (Task 6).
- Produces:
  - `export function currentCueText(cues: CueText[], time: number): string | null` in `narration.ts` — the cue covering `time`, or `null` between cues.
  - `export function NarrationSubtitles({ video, active }: { video: HTMLVideoElement | null; active: boolean })` in `ui/`.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/watch/application/narration-current.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { currentCueText } from './narration'
import type { CueText } from './narration-vtt'

const cue = (start: number, end: number, text: string): CueText =>
  ({ start, end, text }) as CueText

describe('currentCueText', () => {
  const cues = [cue(0, 2, 'a'), cue(10, 12, 'b')]

  it('returns the cue covering the moment', () => {
    expect(currentCueText(cues, 11)).toBe('b')
  })

  it('returns null in the silence between cues', () => {
    // Leaving the last line on screen through a pause reads as a stuck player.
    expect(currentCueText(cues, 5)).toBeNull()
  })

  it('returns null before the first cue', () => {
    expect(currentCueText(cues, -1)).toBeNull()
  })

  it('returns null when there are no cues', () => {
    expect(currentCueText([], 5)).toBeNull()
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/features/watch/application/narration-current.test.ts`
Expected: FAIL — `currentCueText` is not exported.

- [ ] **Step 3: Write minimal implementation**

Add to `web/src/features/watch/application/narration.ts`:

```ts
/** The cue covering `time`, or null when nothing is being said. */
export function currentCueText(cues: CueText[], time: number): string | null {
  for (const c of cues) {
    if (time >= c.start && time < c.end) return c.text
  }
  return null
}

/** The parsed cues, for callers that render rather than speak. */
export function narrationCues(): CueText[] {
  return _cues ?? []
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/features/watch/application/narration-current.test.ts`
Expected: PASS — 4 tests.

- [ ] **Step 5: Write the overlay component**

Create `web/src/features/watch/ui/NarrationSubtitles.tsx`:

```tsx
import { useEffect, useState } from 'react'
import {
  currentCueText,
  narrationCues,
  translatedCue,
} from '@/features/watch/application/narration'

/**
 * Draws the machine translation of the line currently being spoken.
 *
 * An overlay rather than a <track>: these cues are translated in the browser
 * and never become a subtitle file, so there is nothing for the caption
 * renderer to be handed.
 *
 * Nothing is drawn until the translation exists. Showing the English would be
 * showing the wrong language under a Vietnamese-subtitles setting, and showing
 * a placeholder would flicker on every cue the background pass has not reached.
 */
export function NarrationSubtitles({
  video,
  active,
}: {
  video: HTMLVideoElement | null
  active: boolean
}) {
  const [line, setLine] = useState<string | null>(null)

  useEffect(() => {
    if (!active || !video) {
      setLine(null)
      return
    }
    // Four times a second: fast enough that a line lands with the voice, slow
    // enough to stay off the render path of a playing video.
    const id = window.setInterval(() => {
      const en = currentCueText(narrationCues(), video.currentTime)
      setLine(en ? (translatedCue(en) ?? null) : null)
    }, 250)
    return () => window.clearInterval(id)
  }, [active, video])

  if (!line) return null

  return (
    <div className="pointer-events-none absolute inset-x-0 bottom-16 z-20 flex justify-center px-4">
      <span className="rounded bg-black/70 px-2 py-1 text-center text-base leading-snug text-white sm:text-lg">
        {line}
      </span>
    </div>
  )
}
```

- [ ] **Step 6: Render it in the player**

In `Player.tsx`, import the component and render it inside the element that already holds the video and its controls (the container with `relative` that the controls bar is positioned against), passing the front video element:

```tsx
        <NarrationSubtitles video={videoRef.current} active={narrationShows} />
```

- [ ] **Step 7: Verify**

Run: `cd web && npx tsc --noEmit && npx vitest run src/features/watch/`
Expected: no type errors, all tests PASS.

- [ ] **Step 8: Commit**

```bash
git add web/src/features/watch/ui/NarrationSubtitles.tsx \
        web/src/features/watch/application/narration.ts \
        web/src/features/watch/application/narration-current.test.ts \
        web/src/features/watch/ui/Player.tsx
git commit -m "feat: show the translated line for subtitle narration modes"
```

---

### Task 9: Correct the measurements in the handoff document

`HANDOFF.md` currently states NLLB runs at "~0.5s/câu sau warmup". Measured warm on this machine it is 1.3–1.7s for a 17–20 word cue — roughly three times slower than documented. A wrong number in a document that exists to save the next person from measuring is worse than no number.

**Files:**
- Modify: `web/src/features/watch/application/HANDOFF.md:165-190` (the "Translation Models" section)

- [ ] **Step 1: Replace the Translation Models section**

Replace the section between `## Translation Models` and `## Unit Tests` with:

````markdown
## Translation Models

Config: `services/translate_server.py`. Port 8005. Two engines, chosen in the
player's settings menu under "Máy dịch".

All figures measured on this machine (M4, 24 GB) on 2026-08-03, using cues
grouped by the real `parseVTT()`.

| | NLLB-200-distilled-600M | Qwen3-8B-4bit (MLX) |
|---|---|---|
| Path | one request per cue | batch of 15, 3 cues of context |
| Throughput | ~10 words/s | 7.43 words/s |
| Per cue | 1.34s (20 words), 1.71s (17 words) | 1.36s |
| Cold first call | 13.3s | 3.5s model load |
| Resident | ~4 GB | ~4.5 GB |
| Addresses viewer as "bạn" | only via the marker hack (2/5 wrong without) | 5/5 from the prompt |
| Context between cues | none | yes |

**MLX cannot run NLLB.** NLLB-200 is encoder-decoder (M2M100) and `mlx-lm`
ships no such architecture — all 120 model files are decoder-only. Porting it
would mean writing the model. This was checked, not assumed.

**Switching engines does not make translation faster.** Both MLX candidates
measured *slower* per cue than NLLB. The speed-up comes entirely from caching
results in `{MEDIA_ROOT}/{videoId}/narration.vi.json`; the engine choice buys
quality. Do not reach for a bigger model to fix a latency complaint.

**Batch misalignment is caused by fragmented input, not by the model.** Fed
deduplicated raw VTT lines, Qwen returned 9 of 12 numbered lines and Gemma-3-12B
44 of 60, silently shifting every later line onto the wrong cue. Fed cues
grouped by `parseVTT()`, both returned 60/60. Any future benchmark must use
grouped cues or it measures the wrong thing. The server still refuses a batch
whose line count does not match and retries per cue.

**Gemma-3-12B was measured and rejected.** Its Vietnamese is the most natural of
the three, but it invents content: `"Just think about that."` came back as
`"Nghĩ xem, đúng là bất ngờ."` A viewer listening to narration has no way to
tell an invented sentence from the speaker's own. It is also 1.7× slower
(4.32 words/s), takes 33s to load, and needs 7 GB — which does not fit on the
internal disk, only on the external SSD.

**Speech density**, measured across six real subtitle files: 1.1–3.1 words per
second of video. So Qwen stays ahead of playback by 2.4–3.7× and NLLB by more.
That margin is not unlimited: pressing play also starts a yt-dlp download and an
ffmpeg remux competing for the same GPU.

### Changing model

1. Edit `QWEN_MODEL` (or `NLLB_MODEL`) in `services/translate_server.py`.
2. Restart: `/tmp/nllb-venv/bin/python services/translate_server.py &`
3. The cache is partitioned by engine id — a new model reusing an old id will
   read the previous model's answers. Give it a new id.
````

- [ ] **Step 2: Verify no stale claim survives**

Run: `grep -n "0.5s\|nllb_server" web/src/features/watch/application/HANDOFF.md`
Expected: no matches.

- [ ] **Step 3: Commit**

```bash
git add web/src/features/watch/application/HANDOFF.md
git commit -m "docs: correct translation timings and record why MLX cannot run NLLB"
```

---

## Manual verification before calling this done

Run the stack with `scripts/dev.sh`, open a video whose subtitles are English only, and check each of these:

1. **Voice, batch engine.** Settings → Thuyết minh → "Chỉ giọng đọc", Máy dịch → Qwen. First spoken line arrives within a few seconds, not twenty. Watch the sidecar log for `[batch qwen] 3 cues` followed by `15 cues`.
2. **Cache is written.** `cat "$MEDIA_ROOT/<videoId>/narration.vi.json" | head -c 300` shows a `"qwen"` partition.
3. **Cache is used.** Reload the page and turn narration on again. The sidecar logs no new batches for cues already covered, and the first line is immediate.
4. **Resume starts at the playhead.** Seek to the middle before enabling narration; the first batch logged should be around that point, not the opening.
5. **Engine switch is clean.** Switch to NLLB. The next line is translated by the realtime path, and the file grows an `"nllb"` partition without the `"qwen"` one shrinking.
6. **Subtitles mode.** Switch output to "Chỉ phụ đề tiếng Việt": Vietnamese text appears over the video, the video's own audio is **not** ducked, and no TTS plays.
7. **Both.** Text and voice together, audio ducked as before.
8. **Failure is survivable.** Kill the sidecar mid-video. Playback continues, no error dialog, narration simply stops producing new lines.
