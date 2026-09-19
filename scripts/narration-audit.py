#!/usr/bin/env python3
"""Find videos whose stored narration was translated one line out of step.

The fault this looks for is recorded in CLAUDE.md: the batch translator asked a
model for N numbered lines and checked only that N came back. A model that
merged two cues into one line had to invent a line to keep the count, and from
that point on every cue carried the *next* cue's words — narration arriving
before the speaker does. The guard now refuses such a batch, but the answers it
let through are already on disk, and a cache hit is never re-translated.

Read-only unless --delete is passed. What it deletes is the translation cache
and the machine-translated subtitle, which is what forces the next pass to ask
again; the synthesised WAVs are keyed by the text that produced them, so a wrong
line's audio is simply never asked for again.

    scripts/narration-audit.py [--media-root DIR] [--delete]
"""
import argparse
import json
import os
import re
import sys

# The signatures the server's own guard refuses, so a video this reports is a
# video that would be refused today. `numbers_in` is imported rather than copied
# for that reason: two spellings of what counts as a number would be two answers
# to the same question, and the one here is the one nobody runs.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "services"))
from translate_server import borrowed_number  # noqa: E402


def blocks(vtt: str) -> list[str]:
    """The text of each cue in a WebVTT file, in order."""
    out = []
    for block in vtt.split("\n\n"):
        if "-->" not in block:
            continue
        lines = block.strip().split("\n")
        body = [ln for ln in lines[1:] if ln.strip()]
        if body:
            out.append(" ".join(body).strip())
    return out


# A pass sends fifteen cues at a time, so a shift can only repeat a line inside
# one such window. Compared across a whole video the same short line comes back
# legitimately all the time — "Yeah." and "Yep." are both "Ừ." — and measured on
# this library that read 87 of 207 videos as shifted, nearly all of them wrongly.
BATCH = 15

# Short lines collide honestly; long ones do not. A sentence of this length
# repeated for two different cues inside one batch is the invented line a shift
# leaves behind.
DISTINCTIVE = 40


def suspect(cues: list[str], translations: list[str]) -> str:
    """Why this video's translations look shifted, or "".

    Only the repeat signature, and not the length ratio the server also applies:
    that one compares a line against the cue it translates, and here the pairing
    is exactly what is in doubt.
    """
    for i, line in enumerate(translations):
        text = line.strip()
        if len(text) < DISTINCTIVE or i >= len(cues):
            continue
        key = text.casefold()
        for j in range(i + 1, min(i + BATCH, len(translations))):
            if j >= len(cues):
                break
            if translations[j].strip().casefold() != key:
                continue
            if cues[i].strip() == cues[j].strip():
                continue
            return f'cue {i + 1} and cue {j + 1} share one line: "{text[:60]}"'
    return ""


def borrowed(cues: list[str], translations: list[str]) -> str:
    """The server's own borrowed-number signature, worded for a video.

    The line it names is quoted, because the whole point of running this over a
    library is deciding which reports to believe, and a cue number alone sends
    somebody back to the files to find out what it says.
    """
    why = borrowed_number(cues, translations)
    if not why:
        return ""
    why = why.replace("line ", "cue ", 1)
    at = int(why.split()[1]) - 1
    if 0 <= at < len(translations):
        why += f': "{translations[at][:60]}"'
    return why


# The symbols a synthesiser would read out, which the parser drops before a cue
# is ever cached. Kept as escapes: written as literals these four quote marks
# were once flattened to straight ones by an editor, and the class then stripped
# the apostrophe out of every contraction it saw.
SYMBOLS = "\u266a\u266b\u266c\u2192\u2190\u2191\u2193\u2194\u00ab\u00bb" \
          "\u201C\u201D\u2018\u2019\u201e\u201a"


def clean(text: str) -> str:
    """The captions as the parser left them before the cue was cached.

    Entities first, so &gt;&gt; is >> in time to be recognised below — the same
    order `cleanCueText` uses, and for the same reason. Without this step a
    transcript read raw does not contain the text any cue was built from:
    measured on kXVt4atqMv8, 25 of 183 cues could not be placed, every one of
    them because the captions spell "A&C" as "A&amp;C".
    """
    for entity, char in (("&amp;", "&"), ("&lt;", "<"), ("&gt;", ">"),
                         ("&quot;", '"'), ("&#39;", "'")):
        text = text.replace(entity, char)
    text = re.sub(r"<[^>]+>", "", text)
    text = re.sub(r">>\s*", "", text)
    text = text.translate({ord(c): None for c in SYMBOLS})
    return re.sub(r"\s+", " ", text)


def order_from_transcript(cache: dict, vtt: str) -> list[str]:
    """The cached cues in the order they are spoken, read off the captions.

    `narration-cues.json` is the authoritative order and 174 of this library's
    narrated videos do not have one — including the video this check was written
    for. Every cached key is a run of words inside the transcript, so its
    position there is its position in the pass, and that needs no extra file.

    **It places 92% of them, not all.** The rolling format repeats the previous
    cue above each new one, so the repeats have to be dropped — and a cue that
    began in the tail of a dropped line no longer has a run to be found in. That
    is a limit of reading the order back out rather than being handed it, and it
    is recorded rather than worked around: the alternative is the parser itself,
    which lives in Go. A cue that cannot be placed is a cue this audit does not
    examine, so a clean report over these videos is weaker than a clean report
    over one with a cues file.
    """
    lines, seen = [], set()
    for raw in vtt.split("\n"):
        if "-->" in raw or raw.startswith(("WEBVTT", "Kind:", "Language:")):
            continue
        # Cleaned per line: `clean` collapses every run of whitespace, so
        # running it over the whole file first would leave nothing to split on.
        line = clean(raw).strip()
        if not line:
            continue
        # The rolling format repeats the previous cue above each new one.
        if line in seen:
            continue
        seen.add(line)
        lines.append(line)
    text = " ".join(lines)

    placed = []
    for key in cache:
        at = text.find(clean(key).strip())
        if at >= 0:
            placed.append((at, key))
    return [key for _, key in sorted(placed)]


def pairs(folder: str) -> tuple[list[str], list[str]]:
    """(cues, translations) in order, or two empty lists.

    Prefers `narration-cues.json` and the vi-mt subtitle, which is what the
    repeat signature has always read. Falls back to the translation cache, which
    every narrated video has, so a video missing those two is examined rather
    than skipped.
    """
    cues_path = os.path.join(folder, "narration-cues.json")
    subs = [f for f in os.listdir(folder) if f.endswith(".vi-mt.vtt")]
    if os.path.isfile(cues_path) and subs:
        cues = [c["text"] for c in json.load(open(cues_path))]
        with open(os.path.join(folder, subs[0])) as fh:
            return cues, blocks(fh.read())

    cache_path = os.path.join(folder, "narration.vi.json")
    source = [f for f in os.listdir(folder)
              if f.endswith(".vtt") and not f.endswith(".vi-mt.vtt")]
    if not os.path.isfile(cache_path) or not source:
        return [], []
    cache = json.load(open(cache_path)).get("omniroute:sub_translation", {})
    with open(os.path.join(folder, source[0])) as fh:
        cues = order_from_transcript(cache, fh.read())
    return cues, [cache[c] for c in cues]


def audit(root: str, delete: bool) -> int:
    hit = 0
    checked = 0
    for video in sorted(os.listdir(root)):
        folder = os.path.join(root, video)
        if not os.path.isdir(folder):
            continue
        if not os.path.isfile(os.path.join(folder, "narration.vi.json")):
            continue

        try:
            cues, translations = pairs(folder)
        except (OSError, ValueError, KeyError) as err:
            print(f"{video}: unreadable ({err})")
            continue
        if not cues:
            continue

        checked += 1
        why = suspect(cues, translations) or borrowed(cues, translations)
        if not why:
            continue

        hit += 1
        print(f"{video}: {why}")
        if not delete:
            continue

        subs = [f for f in os.listdir(folder) if f.endswith(".vi-mt.vtt")]
        for name in [*subs, "narration.vi.json"]:
            path = os.path.join(folder, name)
            if os.path.exists(path):
                os.remove(path)
                print(f"  removed {name}")

    print(f"\n{hit} of {checked} videos with narration look shifted")
    return hit


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--media-root",
                    default=os.environ.get("MEDIA_ROOT", "/Volumes/Data2/Youtube"))
    ap.add_argument("--delete", action="store_true",
                    help="remove the translation cache and vi-mt subtitle of "
                         "every video reported, so the next pass asks again")
    args = ap.parse_args()
    audit(args.media_root, args.delete)


if __name__ == "__main__":
    main()
