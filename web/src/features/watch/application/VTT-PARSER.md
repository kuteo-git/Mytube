# VTT Parser — YouTube Auto-Caption & Manual Subtitle Parser

Parses WebVTT subtitle files into clean, timed cues for the TTS narration pipeline.
Handles both YouTube auto-captions and manual subtitles, with word-level timing extraction.

## Architecture

```
VTT file (fetch)
  → parseVTTTime()           timestamp string → seconds
  → fetchAndParseVTT()       raw VTT → CueText[] (word-level sub-cues)
  → group & split            accumulate & split at clause boundaries
  → cleanCueText()           strip HTML entities, WebVTT tags, artifacts
  → stripBrackets()          remove [sound-effect] descriptions (post-grouping)
  → CueText[]                final cues ready for TTS scheduling
```

## Cue Format

```ts
interface CueText {
  start: number  // seconds, word-level precise for auto-captions
  end: number    // seconds
  text: string   // clean Vietnamese or English text
}
```

---

## Phase 1: Line-level Parsing (`fetchAndParseVTT`)

### Input: Raw VTT

```
WEBVTT
Kind: captions
Language: vi

00:00:00.000 --> 00:00:01.299 align:start position:0%
 
Được <00:00:00.155><c>rồi, </c><00:00:00.310><c>tôi </c><00:00:00.465><c>có </c>...

00:00:01.299 --> 00:00:01.309 align:start position:0%
Được rồi, tôi có
```

### Step 1: Skip header
Skip all lines until the first `-->` timing line.

### Step 2: Main parse loop
For each cue:

#### a) Parse timing line
```
00:00:00.000 --> 00:00:01.299 align:start position:0%
```
- Extract `start` and `end` (HH:MM:SS.mmm → seconds)
- Everything after the first whitespace following `-->` is ignored (`align:start position:0%`)

#### b) Collect payload lines
- **Skip leading blank lines** — but NEVER skip timing lines (guard: `!lines[i].includes('-->')`)
- Collect payload until a blank line or next timing line
- **Only advance past blank separator when payload is non-empty** — prevents consuming the next timing line

#### c) Detect caption type

| Condition | Type | Behavior |
|-----------|------|----------|
| Any line has `<c>` tags | Auto-caption (tagged) | Parse word-level timestamps |
| No `<c>`, exactly 2 payload lines | Auto-caption (carry-over) | Line 1 = prev clean text (discard), line 2 = new text |
| No `<c>`, 1 or 3+ lines | Manual caption | Join all lines as-is |

#### d) Filter 10ms clean-snapshot cues
Cues with `end - start < 0.1` are discarded. These are YouTube's duplicate clean copies.

---

## Phase 2: Word-Level Timing (Auto-Captions Only)

YouTube auto-captions embed per-word timestamps in the tagged line:

```
Được <00:00:00.155><c>rồi, </c><00:00:00.310><c>tôi </c>...
```

### Extraction rules:

1. **Leading text** before the first `<timestamp>` tag:
   - Regex: `/^([^<]+)</`
   - Example: `"Được "` → sub-cue at `start` (cue start time)

2. **`<timestamp><c>text</c>` pairs**:
   - Regex: `/(\d{2}:\d{2}:\d{2}\.\d{3})><c>([^<]*)<\/c>/g`
   - Each match → sub-cue with precise `start` time
   - Text is cleaned via `cleanCueText()`

3. **Fallback** — if tagged line has no `<c>` tags and no leading text:
   - Example: `"giúp đỡ."` or `"?"` on its own line (2-line carry-over without tags)
   - Use `cleanCueText(taggedLine)` as a single cue

### End time assignment:
Each sub-cue's `end` = next sub-cue's `start`. Last sub-cue's `end` = cue's `end`.

---

## Phase 3: Clause Grouping & Splitting

Accumulate cues into a text buffer, split at clause boundaries.

### Boundary detection

| Punctuation | Rule |
|-------------|------|
| `.` | Sentence end. **Excluded**: decimal numbers (`2.5`, `3.14`), abbreviations (`Dr.`, `Mr.`, `DR.`, `Prof.`, etc.) |
| `!` `?` | Always split |
| `,` | Split only when **both sides** have > 2 words. Prevents orphan fragments like `"Then,"` or `", maybe"` |

### Forced split
If buffer reaches **30 words** without any punctuation, force a split.

### Timing
Clause `start` = first word's start. Clause `end` = last word's start (not its scheduled end, which is the next word's start). This gives natural gaps between sentences.

### Post-processing: `stripBrackets()`
Applied AFTER grouping, not per-word:
- Removes `[sound-effect]` brackets (e.g., `[tiếng vỗ tay]`, `[âm nhạc]`)
- Preserves emotion tags: `[cười]`, `[thở dài]`, `[hắng giọng]`
- Collapses resulting whitespace, fixes orphaned periods (`" ."` → `"."`)

---

## Phase 4: Text Cleaning

### `cleanCueText()` — per-word/phrase
1. Decode HTML entities: `&amp;` → `&`, `&gt;` → `>`, etc.
2. Remove WebVTT angle-bracket tags: `<c>`, `</c>`, `<00:00:00.000>`
3. Remove `>>` speaker indicators
4. Strip music notes: `♪♫♬` and other non-speech symbols
5. Collapse multiple spaces

### `stripBrackets()` — post-grouping
1. Save emotion tags `[cười]`, `[thở dài]`, `[hắng giọng]` (preserved for TTS)
2. Remove all other `[...]` with surrounding whitespace
3. Restore emotion tags

---

## Edge Cases Handled

| Case | Solution |
|------|----------|
| Leading whitespace before tagged text | Skip blank lines (guarded: never skip timing lines) |
| 10ms clean snapshot cues | Filtered by duration (`< 0.1s`) |
| `<c>` tags spanning brackets `[tiếng <c>vỗ </c><c>tay]` | Bracket stripping deferred to post-grouping |
| 2-line carry-over WITHOUT `<c>` tags | Detected by `isTwoLineCarry`: use line 2 only |
| `?` on its own line | Fallback: `cleanCueText("?")` → `"?"` appended to prev clause |
| `"giúp đỡ."` / `"béo."` — new words without tags | Same fallback |
| Decimal numbers `"2.5"`, `"3.14"` | Period not treated as sentence boundary |
| Abbreviations `"Dr."`, `"Mr."`, `"DR."` | Period not treated as sentence boundary |
| Consecutive blank lines before payload | Loop skip with timing-line guard |
| Empty payload (all whitespace) | Don't advance `i`, let main loop find next timing line |
| 30+ words without punctuation | Force split |

---

## Test Coverage

Unit tests: `narration.test.ts` (22 tests)
- Grouping logic: clause splitting, comma rules, abbreviation handling
- 2-line carry-over: `?`, `giúp đỡ.`, `béo.` fallback
- Word-level timing: leading text, precise timestamps
- Warm-start skip: initial 10s, seek detection
- End-to-end: VTT file parsing (`MeplqZ0nM1c`)

Run: `npx vitest run web/src/features/watch/application/narration.test.ts`
