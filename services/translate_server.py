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


# How much longer than its source a translation may be before the line is read
# as two cues merged into one.
#
# Vietnamese runs longer than English for the same meaning, so the threshold is
# generous: the fault being caught doubles a line, and a merge of two cues of
# any length lands far past this. The constant term keeps very short lines out
# of it — "Yeah." to "Ừ, đúng rồi đấy." is a ratio of 3 and is not a merge.
MERGE_RATIO = 2.4
MERGE_FLOOR = 24

# How many lines before a cue travel with it when it is retried on its own.
#
# Three, which is what the gateway sends for a batch (`narrationContext`). It is
# written here as well rather than derived from what the caller sent, because
# the first batch of a video has nothing before it and the cues in front of a
# line are still that line's context — deriving it would leave exactly that
# batch translating in a vacuum.
CONTEXT_LINES = 3


def context_for(context: list[str], cues: list[str], i: int) -> list[str]:
    """The lines a single-cue retry should see before cue `i`.

    The caller's context runs out as the batch is walked and the batch's own
    earlier cues take its place, so a line late in a batch is read against its
    real neighbours rather than against three lines from before all of them.

    Measured on one real line, against the running router:

        "So, if you're coming from the three or the twos,"
          alone  -> "Vậy nếu bạn đến từ đường ba hoặc đường hai,"
          with 3 -> "Vậy nếu bạn đang dùng thế hệ ba hay hai thì"

    "đường ba" is road number three. Nothing in the line itself says these are
    AirPods, and Vietnamese has to choose a word where English left none.
    """
    return [*context, *cues[:i]][-CONTEXT_LINES:]

# English number words, so a line that says "the twos" counts as carrying a 2.
#
# Without these the check below would fire on every correct translation that
# writes a spelled-out number as a digit, which Vietnamese does as a matter of
# course: "coming from the three or the twos" is correctly "dùng thế hệ 3 hay 2".
NUMBER_WORDS = {
    "zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
    "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11,
    "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
    "sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19,
    "twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60,
    "seventy": 70, "eighty": 80, "ninety": 90, "hundred": 100,
    # A month is a number to a Vietnamese reader: July is "tháng 7". Measured
    # over this library these were the single largest source of false alarms.
    "january": 1, "february": 2, "march": 3, "april": 4, "may": 5, "june": 6,
    "july": 7, "august": 8, "september": 9, "october": 10, "november": 11,
    "december": 12,
}


def digit_runs(text: str) -> list[str]:
    """Every run of digits in the text, as written."""
    return re.findall(r"\d+", text)


def numbers_in(text: str) -> set[int]:
    """Every number the text carries, written as a digit or as a word.

    Plurals count: "the twos" and "the threes" are how a reviewer says it, and
    they are the same fact as "2" and "3".
    """
    found = {int(d) for d in digit_runs(text)}
    for word in re.findall(r"[A-Za-z]+", text.casefold()):
        value = NUMBER_WORDS.get(word) or NUMBER_WORDS.get(word.rstrip("s"))
        if value is not None:
            found.add(value)
    return found


def borrowed_number(cues: list[str], out: list[str]) -> str:
    """Why a line holds a figure belonging to the cue after it, or "".

    The two signatures in `misaligned` both look for the padding a shift leaves
    behind. A model that returns the right number of lines, each the right
    length, attached to the wrong cues leaves none — and both go blind. Measured
    on kXVt4atqMv8, where six consecutive cues carried the next cue's words:
    every line distinct, the longest 54 characters against a threshold of 122.

    So this one reads the contents instead of the envelope, which is the lesson
    the first fix recorded one level up. A number is what survives translation
    unchanged, so a line holding a figure that belongs to the cue after it did
    not come from the cue it is filed under.

    The audit script calls this too, which is why it is a function of its own:
    two spellings of what counts as a borrowed number would be two answers to
    the same question, and the one in the script is the one nobody runs.
    """
    for i in range(min(len(out), len(cues)) - 1):
        source = digit_runs(cues[i])
        mine = numbers_in(cues[i])
        theirs = numbers_in(cues[i + 1])
        borrowed = set()
        for run in digit_runs(out[i]):
            value = int(run)
            if value in mine or value not in theirs:
                continue
            # The same number written another way. "how old were you on 911"
            # becomes "vào ngày 11/9", which is the question's own digits laid
            # out for a reader who puts the day first — not a figure from
            # anywhere else.
            if any(run in whole for whole in source):
                continue
            borrowed.add(value)
        if borrowed:
            return f"line {i + 1} carries {sorted(borrowed)} from cue {i + 2}"
    return ""


def misaligned(cues: list[str], out: list[str]) -> str:
    """Why this batch's lines do not belong to these cues, or "".

    Counting the lines is not checking the alignment, and the difference cost a
    library of narration read one sentence ahead of the film. Measured on a real
    answer: the model merged cues 1 and 2 into line 1, then repeated line 2 as
    line 3 to keep the count right. Every number was present, every number was
    in order, and from line 2 on every cue spoke the *next* cue's words — which
    is heard as narration arriving before the speaker does.

    Both signatures below are of that one fault. A model that shifts the content
    has to make up a line somewhere to keep the count, and it either repeats one
    it has already written or joins two into the line before.
    """
    # A line repeated for two cues that are not themselves the same line. This
    # is the padding a shift leaves behind. Blank answers are excluded: an
    # untranslated line is already handled as absence by the caller.
    seen: dict[str, int] = {}
    for i, line in enumerate(out):
        key = line.strip().casefold()
        if not key:
            continue
        first = seen.get(key)
        if first is not None and cues[first].strip() != cues[i].strip():
            return f"line {first + 1} repeated as line {i + 1}"
        seen.setdefault(key, i)

    # A line far longer than the one it translates, which is two cues joined.
    for i, line in enumerate(out):
        source = len(cues[i].strip())
        if not source:
            continue
        if len(line.strip()) > MERGE_FLOOR + MERGE_RATIO * source:
            return f"line {i + 1} is {len(line.strip())} chars for {source}"

    return borrowed_number(cues, out)


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
    out = aligned_or_none(parse_numbered(openai_content(data)), len(cues))
    if out is None:
        return None
    # Only a batch can be misaligned. A single cue has no ordering to get wrong,
    # and it is what the caller retries with when this refuses — so applying the
    # check there would turn one suspicious batch into no translation at all.
    if len(cues) > 1:
        why = misaligned(cues, out)
        if why:
            print(f"[batch {model}] refused: {why}", flush=True)
            return None
    return out


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
            # So does the context, for the same kind of reason. What this retry
            # removes is the ordering a batch can get wrong; the lines around a
            # cue are not part of that. A context line is marked "do NOT
            # translate" and is not counted, so it cannot reintroduce the shift
            # — and without it every pronoun and every bare noun phrase is
            # resolved by guesswork.
            single = omniroute_batch([c], context_for(context, cues, i),
                                     base_url, model, api_key, one_slot)
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
