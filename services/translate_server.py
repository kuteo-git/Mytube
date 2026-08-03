"""EN -> VI translation for narration. Default port 8005.

Translation goes to an OpenAI-compatible router on the LAN. The local models
that used to live here — NLLB-200 on MPS and Qwen3-8B through MLX — have been
removed: the router translates at least as well, starts answering sooner
because there is no model to load, and leaves the GPU to yt-dlp and ffmpeg,
which are already using it every time someone presses play.

What is left is the part that was never about the model: building the prompt,
reading numbered lines back out, and refusing a batch that does not line up.
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


def resolve_config(body: dict) -> tuple[str, str, str]:
    """Where to send this batch, and under whose key.

    The gateway owns the configuration and sends it down with every batch, which
    is what lets a change take effect on the next batch instead of on a restart.
    The environment stays the fallback, so a deployment nobody has configured
    from the app behaves exactly as .env.local says.

    Blank fields fall back rather than being taken literally — an empty base url
    is a field the caller left alone, not a request to translate against nothing.
    """
    return (
        (body.get("baseUrl") or "") or OMNIROUTE_BASE_URL,
        (body.get("model") or "") or OMNIROUTE_MODEL,
        (body.get("apiKey") or "") or OMNIROUTE_API_KEY,
    )


def omniroute_batch(
    cues: list[str],
    context: list[str],
    base_url: str = "",
    model: str = "",
    api_key: str = "",
) -> list[str] | None:
    """Translate a batch through the LAN router.

    `stream: False` is required, not optional: the server streams by default
    even when nothing asked it to, and a body of SSE frames is not JSON.
    """
    base_url = base_url or OMNIROUTE_BASE_URL
    model = model or OMNIROUTE_MODEL
    api_key = api_key or OMNIROUTE_API_KEY
    if not base_url:
        return None
    ctx = ""
    if context:
        ctx = "\nNgữ cảnh (các câu ngay trước, KHÔNG dịch):\n" + \
              "\n".join(f"- {c}" for c in context) + "\n"
    body = "\n".join(f"{i + 1}. {c}" for i, c in enumerate(cues))
    payload = {
        "model": model,
        "stream": False,
        # Thinking off. sub_translation routes to a reasoning model, and the
        # reasoning was being paid for and then thrown away: clean() strips the
        # <think> block and openai_content() reads `content` only.
        #
        # This is the field the router honours, and it is worth knowing that it
        # is the *only* one. Measured on a real 15-cue batch, both of the usual
        # switches came back with the reasoning still attached and no change in
        # timing: chat_template_kwargs.enable_thinking=false, and "/no_think"
        # appended to the prompt. On the same batch this field took it from
        # 5.1s and 649 completion tokens to 2.5s and 345, with the translation
        # unchanged.
        "thinking": {"type": "disabled"},
        "messages": [{
            "role": "user",
            "content": BATCH_PROMPT.format(n=len(cues), ctx=ctx, body=body),
        }],
    }
    req = urllib.request.Request(
        base_url.rstrip("/") + "/v1/chat/completions",
        json.dumps(payload).encode(),
        {
            "Authorization": f"Bearer {api_key}",
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


# ---- app --------------------------------------------------------------------

app = FastAPI()
app.add_middleware(CORSMiddleware, allow_origins=["*"],
                   allow_methods=["*"], allow_headers=["*"])


@app.get("/health")
def health():
    return {
        "status": "ok",
        "configured": bool(OMNIROUTE_BASE_URL and OMNIROUTE_API_KEY),
        "model": OMNIROUTE_MODEL,
    }


@app.post("/translate/batch")
async def translate_batch(req: Request):
    body = await req.json()
    cues = list(body.get("cues") or [])
    if not cues:
        return JSONResponse({"translations": [], "fell_back": False})
    context = body.get("context") or []
    base_url, model, api_key = resolve_config(body)

    t0 = time.perf_counter()
    fell_back = False

    out = omniroute_batch(cues, context, base_url, model, api_key)
    if out is None:
        # The batch could not be trusted. Retry one cue at a time, where there
        # is no ordering left to get wrong.
        fell_back = True
        out = []
        for c in cues:
            single = omniroute_batch([c], [])
            out.append(single[0] if single else "")

    dt = time.perf_counter() - t0
    words = sum(len(c.split()) for c in cues)
    # flush: stdout is a pipe under dev.sh, and Python buffers pipes. Without
    # this the log this server exists to be watched through stays empty until
    # the process ends.
    print(f"[batch {model}] {len(cues)} cues / {words} words in {dt:.1f}s"
          f"{' FELL BACK' if fell_back else ''}", flush=True)
    return JSONResponse({"translations": out, "fell_back": fell_back})


if __name__ == "__main__":
    print(f"translate server on {PORT} -> {OMNIROUTE_BASE_URL}", flush=True)
    uvicorn.run(app, host="0.0.0.0", port=PORT, access_log=False)
