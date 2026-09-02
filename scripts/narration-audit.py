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
import sys

# The same two signatures the server's own guard refuses, so a video this
# reports is a video that would be refused today.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "services"))


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


def audit(root: str, delete: bool) -> int:
    hit = 0
    checked = 0
    for video in sorted(os.listdir(root)):
        cues_path = os.path.join(root, video, "narration-cues.json")
        if not os.path.isfile(cues_path):
            continue

        subs = [f for f in os.listdir(os.path.join(root, video))
                if f.endswith(".vi-mt.vtt")]
        if not subs:
            continue

        try:
            cues = [c["text"] for c in json.load(open(cues_path))]
            with open(os.path.join(root, video, subs[0])) as fh:
                translations = blocks(fh.read())
        except (OSError, ValueError, KeyError) as err:
            print(f"{video}: unreadable ({err})")
            continue

        checked += 1
        why = suspect(cues, translations)
        if not why:
            continue

        hit += 1
        print(f"{video}: {why}")
        if not delete:
            continue

        for name in [*subs, "narration.vi.json"]:
            path = os.path.join(root, video, name)
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
