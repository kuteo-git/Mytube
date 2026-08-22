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

# The prompt is written in English because the model spends fewer tokens reading
# it that way, and this text is prepended to every batch of every video.
#
# The budget in brackets is the one part that is not a style preference. These
# lines are read aloud over a video, so each has only the time until the next
# subtitle. A translation that runs long is not trimmed — it is *sped up*, and
# past about 3x Vietnamese TTS stops being followable, at which point the line is
# dropped entirely. Telling the model how much room each line has moves that
# problem to where it can actually be solved: shorter phrasing, not faster
# speech. Lines with room to spare are translated normally, which is why the
# budget is stated per line rather than as one blanket instruction to be terse.
BATCH_PROMPT = """Translate the following English subtitles into Vietnamese.

Rules:
- Address the viewer as "bạn". Never "anh", "chị" or "quý vị".
- Natural spoken Vietnamese. Do not translate word by word.
- Translate what the line says. Do NOT add ideas, do NOT comment.
- Keep English technical terms that Vietnamese speakers already use; translate
  ordinary words.
- Translate idioms, slang and figures of speech by their MEANING, never
  literally. A literal idiom is worse than a plain paraphrase.
- Each line is prefixed with the seconds available to say it, like "[2.4s]".
  Treat it as a budget: the Vietnamese must be short enough to be spoken
  comfortably in that time at a normal pace.
  - To fit, drop filler, circumlocution and repeated subjects, and prefer the
    shorter of two correct wordings.
  - NEVER drop information to fit. Meaning outranks the budget.
  - A line with plenty of time needs no shortening at all.
- Do NOT repeat the "[2.4s]" prefix in your answer.
- Return EXACTLY {n} lines, each as "number. translation". No explanations.
{ctx}
To translate:
{body}"""


# ---- prompt building --------------------------------------------------------

def build_body(cues: list[str], slots: list[float] | None) -> str:
    """The numbered lines to translate, each with the time it has to be said in.

    Without slots this is the plain numbered list it always was — callers that
    do not know the timing (the single-cue retry path) still work.
    """
    out = []
    for i, cue in enumerate(cues):
        slot = None
        if slots and i < len(slots):
            slot = slots[i]
        if slot and slot > 0:
            out.append(f"{i + 1}. [{slot:.1f}s] {cue}")
        else:
            # The last cue of a video has no following cue, so no budget. An
            # empty "[]" would read as a budget of nothing and invite the model
            # to translate it to a word.
            out.append(f"{i + 1}. {cue}")
    return "\n".join(out)


# A duration prefix the model copied out of the prompt instead of dropping it.
#
# Left in place it would be spoken: parse_numbered puts everything after the
# number into the translation, and the narrator reads that aloud. The listener
# hears "hai phẩy bốn giây" in the middle of the film.
#
# Deliberately narrow — a real subtitle may well open with a bracket, so only
# something shaped exactly like a duration is removed.
_BUDGET_PREFIX = re.compile(r"^\[\s*\d+(?:[.,]\d+)?\s*s\s*\]\s*", re.I)


def strip_budget(line: str) -> str:
    return _BUDGET_PREFIX.sub("", line, count=1).strip()


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
        out[int(num)] = strip_budget(body)
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
    slots: list[float] | None = None,
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
        ctx = "\nContext (the lines just before these, do NOT translate):\n" + \
              "\n".join(f"- {c}" for c in context) + "\n"
    body = build_body(cues, slots)
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
    data = _post_chat(chat_url(base_url), payload, api_key)
    if data is None:
        return None
    return aligned_or_none(parse_numbered(openai_content(data)), len(cues))


def _post_chat(url: str, payload: dict, api_key: str):
    """One chat completion, retried once without `thinking` if it is refused.

    `thinking` is not an OpenAI parameter. It is worth sending — measured on a
    real 15-cue batch it took the same translation from 5.1s and 649 completion
    tokens to 2.5s and 345 — and providers that do not know it answer 400
    rather than ignoring it.

    So it is sent, and a 400 that names it is read as "this provider does not
    have that field" rather than as a failure. The alternative was to drop it
    everywhere, which would spend the measured half of the time on every batch
    to accommodate a provider the household does not use.

    Only on a 400, and only when the body mentions the field: a 400 for any
    other reason is a real error and retrying it blind would hide it behind a
    second identical failure.
    """
    body = json.dumps(payload).encode()
    headers = {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"}
    try:
        req = urllib.request.Request(url, body, headers)
        with urllib.request.urlopen(req, timeout=OMNIROUTE_TIMEOUT) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        detail = ""
        try:
            detail = e.read().decode("utf-8", "replace")[:500]
        except Exception:
            pass
        if e.code != 400 or "thinking" not in detail or "thinking" not in payload:
            print(f"[omniroute] {e} {detail}", flush=True)
            return None
        print("[omniroute] provider rejected 'thinking'; retrying without it", flush=True)
        retry = {k: v for k, v in payload.items() if k != "thinking"}
        try:
            req = urllib.request.Request(url, json.dumps(retry).encode(), headers)
            with urllib.request.urlopen(req, timeout=OMNIROUTE_TIMEOUT) as resp:
                return json.load(resp)
        except (urllib.error.URLError, TimeoutError, ValueError) as err:
            print(f"[omniroute] {err}", flush=True)
            return None
    except (urllib.error.URLError, TimeoutError, ValueError) as e:
        print(f"[omniroute] {e}", flush=True)
        return None


def chat_url(base_url: str) -> str:
    """The chat completions endpoint, from whatever was typed in the field.

    This field has always been given a base *without* `/v1` and appended the
    rest itself, while every provider's documentation — and this project's own
    speech field — gives the base *with* it. Two inputs on one settings screen
    disagreeing about what a base URL is, and someone will paste one into the
    other: the result is `/v1/v1/chat/completions` and a 404 that explains
    nothing.

    Both are accepted. A base already naming the endpoint is left alone, for
    somebody who pasted the whole thing.
    """
    trimmed = base_url.rstrip("/")
    if trimmed.endswith("/chat/completions"):
        return trimmed
    if not trimmed.endswith("/v1"):
        trimmed += "/v1"
    return trimmed + "/chat/completions"


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
    # How long each line has before the next is due. Optional: a caller that
    # does not send them gets the plain numbered list and no budget rule.
    slots = list(body.get("slots") or [])
    base_url, model, api_key = resolve_config(body)

    t0 = time.perf_counter()
    fell_back = False

    out = omniroute_batch(cues, context, base_url, model, api_key, slots)
    if out is None:
        # The batch could not be trusted. Retry one cue at a time, where there
        # is no ordering left to get wrong.
        fell_back = True
        out = []
        for i, c in enumerate(cues):
            # The budget travels with the line. Retrying without it would answer
            # a differently-worded question and could hand back a line that no
            # longer fits, which is exactly what the retry is trying to salvage.
            one_slot = [slots[i]] if i < len(slots) else None
            single = omniroute_batch([c], [], base_url, model, api_key, one_slot)
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
