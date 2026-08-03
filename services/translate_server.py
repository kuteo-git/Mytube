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

NLLB cannot be moved to MLX: it is encoder-decoder (M2M100) and mlx-lm ships
only decoder-only architectures. This was checked, not assumed.
"""
import json
import os
import re
import time
import urllib.error
import urllib.request

import uvicorn
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

NLLB_MODEL = os.environ.get("NLLB_MODEL", "facebook/nllb-200-distilled-600M")
QWEN_MODEL = os.environ.get("QWEN_MODEL", "mlx-community/Qwen3-8B-4bit")
PORT = int(os.environ.get("NLLB_PORT", "8005"))

# An OpenAI-compatible router on the LAN. Configured entirely from the
# environment, and the key is never written down here — scripts/dev.sh sources
# .env.local, which is not in git.
OMNIROUTE_BASE_URL = os.environ.get("OMNIROUTE_BASE_URL", "")
OMNIROUTE_MODEL = os.environ.get("OMNIROUTE_MODEL", "sub_translation")
OMNIROUTE_API_KEY = os.environ.get("OMNIROUTE_API_KEY", "")
OMNIROUTE_TIMEOUT = float(os.environ.get("OMNIROUTE_TIMEOUT", "300"))

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


def openai_content(payload: dict) -> str:
    """The assistant's answer out of an OpenAI-shaped response.

    Reads `content` only. sub_translation routes to a reasoning model which also
    returns `reasoning_content`, and that field contains the model talking to
    itself — including, sometimes, numbered lines that would be mistaken for
    translations.
    """
    try:
        return payload["choices"][0]["message"]["content"] or ""
    except (KeyError, IndexError, TypeError):
        return ""


def omniroute_batch(cues: list[str], context: list[str]) -> list[str] | None:
    """Translate a batch through the LAN router.

    `stream: False` is required, not optional: the server streams by default
    even when nothing asked it to, and a body of SSE frames is not JSON.
    """
    if not OMNIROUTE_BASE_URL:
        return None
    ctx = ""
    if context:
        ctx = "\nNgữ cảnh (các câu ngay trước, KHÔNG dịch):\n" + \
              "\n".join(f"- {c}" for c in context) + "\n"
    body = "\n".join(f"{i + 1}. {c}" for i, c in enumerate(cues))
    payload = {
        "model": OMNIROUTE_MODEL,
        "stream": False,
        "messages": [{
            "role": "user",
            "content": BATCH_PROMPT.format(n=len(cues), ctx=ctx, body=body),
        }],
    }
    req = urllib.request.Request(
        OMNIROUTE_BASE_URL.rstrip("/") + "/v1/chat/completions",
        json.dumps(payload).encode(),
        {
            "Authorization": f"Bearer {OMNIROUTE_API_KEY}",
            "Content-Type": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=OMNIROUTE_TIMEOUT) as resp:
            data = json.load(resp)
    except (urllib.error.URLError, TimeoutError, ValueError) as e:
        print(f"[omniroute] {e}", flush=True)
        return None
    return aligned_or_none(parse_numbered(openai_content(data)), len(cues))


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
            "engines": {
                "nllb": _nllb is not None,
                "qwen": _qwen is not None,
                "omniroute": bool(OMNIROUTE_BASE_URL and OMNIROUTE_API_KEY),
            }}


@app.post("/translate")
async def translate(req: Request):
    body = await req.json()
    text = (body.get("text") or "").strip()
    if not text:
        return JSONResponse({"translated": ""})
    t0 = time.perf_counter()
    out = nllb_one(text, body.get("src", "eng_Latn"), body.get("tgt", "vie_Latn"))
    print(f"[{time.perf_counter() - t0:.2f}s] {text[:50]} -> {out[:50]}", flush=True)
    return JSONResponse({"translated": out})


@app.post("/translate/batch")
async def translate_batch(req: Request):
    body = await req.json()
    cues = list(body.get("cues") or [])
    if not cues:
        return JSONResponse({"translations": [], "engine": "", "fell_back": False})
    engine = body.get("engine", "qwen")
    context = body.get("context") or []

    t0 = time.perf_counter()
    fell_back = False

    if engine == "omniroute":
        out = omniroute_batch(cues, context)
        if out is None:
            fell_back = True
            out = []
            for c in cues:
                single = omniroute_batch([c], [])
                out.append(single[0] if single else "")
    elif engine == "qwen":
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
    # flush: stdout is a pipe under dev.sh, and Python buffers pipes. Without
    # this the log this server exists to be watched through stays empty until
    # the process ends.
    print(f"[batch {engine}] {len(cues)} cues / {words} words in {dt:.1f}s"
          f"{' FELL BACK' if fell_back else ''}", flush=True)
    return JSONResponse({"translations": out, "engine": engine,
                         "fell_back": fell_back})


if __name__ == "__main__":
    print(f"translate server on {PORT}; engines load on first use", flush=True)
    uvicorn.run(app, host="0.0.0.0", port=PORT, access_log=False)
