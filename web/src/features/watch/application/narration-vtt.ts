/**
 * WebVTT → narration cues.
 *
 * Pure: a string in, cues out. It was previously welded to a `fetch` call, which
 * meant the only way to try it on a real caption file was to run the app and
 * listen — and listening is how "sometimes a line repeats, sometimes one goes
 * missing" stayed unresolved. Separated, it can be run against the actual files
 * on disk.
 *
 * Two caption shapes arrive from YouTube and they are not variations of one
 * format, they are different formats:
 *
 *   manual    one cue, one or more plain lines, complete sentences already
 *   automatic rolling cues, where each carries the tail of the previous line
 *             plus one new phrase, the new phrase timestamped word by word
 *
 * The automatic shape is the awkward one. Its cues overlap by design, and the
 * per-word timestamps are the only accurate timing in the file.
 *
 * That overlap is handled where it arises — by reading only the new line of a
 * rolling block — and nowhere else. There used to be a pass at the end that
 * compared each cue with the one before it and stripped whatever prefix they
 * shared, if the shared part covered half the earlier cue. It could not tell a
 * rolling repeat from two lines that genuinely begin alike, so it deleted real
 * captions as readily as duplicated ones: one guess producing both the
 * complaints it was meant to prevent. A file where two speakers each say "Come
 * on." loses the second of them to a rule like that. Removing it costs nothing,
 * because reading only the new line leaves nothing for it to find.
 */

export interface CueText {
  start: number
  end: number
  text: string
}

/** WebVTT timestamp → seconds. "00:01:23.456" → 83.456 */
export function parseVTTTime(raw: string): number {
  const parts = raw.split(':')
  if (parts.length !== 3) return NaN
  const h = Number.parseInt(parts[0], 10)
  const m = Number.parseInt(parts[1], 10)
  const s = Number.parseFloat(parts[2])
  return h * 3600 + m * 60 + s
}

/**
 * Strip WebVTT markup and the symbols a speech synthesiser would read out.
 *
 * `[cười]`, `[thở dài]`, `[hắng giọng]` survive: the TTS server understands
 * those as emotion. Other bracketed descriptions are removed by stripBrackets,
 * later, because a bracket can span several `<c>` tags and would not be whole
 * at this point.
 */
export function cleanCueText(raw: string): string {
  let s = raw
    // Entities first, so &gt;&gt; becomes >> in time to be recognised below.
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")

  s = s
    .replace(/<[^>]+>/g, '')
    .replace(/>>\s*/g, '')
    .replace(/[♪♫♬→←↑↓↔«»""''„‚]/g, '')
    .trim()

  return s.replace(/\s{2,}/g, ' ')
}

/** Remove [sound effects] but keep the emotion tags the TTS understands. */
export function stripBrackets(text: string): string {
  const EMOTION_TAGS = /\[(cười|thở dài|hắng giọng)\]/gi
  const placeholders: string[] = []
  let s = text.replace(EMOTION_TAGS, (match) => {
    placeholders.push(match)
    return `\x00${placeholders.length - 1}\x00`
  })
  s = s.replace(/\s*\[[^\]]*\]\s*/g, ' ')
  s = s.replace(/ \./g, '.')
  s = s.replace(/\x00(\d+)\x00/g, (_, i) => placeholders[+i] || '')
  s = s.replace(/\s{2,}/g, ' ')
  return s.trim()
}

/** Cues shorter than this are YouTube's clean-snapshot copies, not speech. */
const SNAPSHOT_MAX_SECONDS = 0.1

/** A clause is not split off a comma if either side is shorter than this. */
const MIN_CLAUSE_WORDS = 3

/** With no punctuation at all, a clause is cut once it reaches this length. */
const FORCE_SPLIT_WORDS = 30

const ABBREVIATIONS = /^(Dr|Mr|Mrs|Ms|DR|Prof|Sr|Jr|vs|etc)$/i

/** One word or phrase with the time it is spoken. */
interface Piece {
  start: number
  end: number
  text: string
}

interface RawBlock {
  start: number
  end: number
  lines: string[]
}

/**
 * Split the file into timed blocks.
 *
 * The payload of a block ends at a blank line *or* at the next timing line —
 * files in the wild have both. Only the blank separator may be stepped over;
 * stepping over a timing line, which the previous version did unconditionally,
 * discards the whole cue that line introduced.
 */
function readBlocks(raw: string): RawBlock[] {
  const lines = raw.replace(/\r\n?/g, '\n').split('\n')
  const blocks: RawBlock[] = []
  let i = 0

  while (i < lines.length) {
    if (!lines[i]?.includes('-->')) {
      i++
      continue
    }

    const timing = lines[i]
    const arrow = timing.indexOf('-->')
    const start = parseVTTTime(timing.slice(0, arrow).trim())
    const end = parseVTTTime(timing.slice(arrow + 3).trim().split(/\s/)[0])
    i++

    // Leading blank lines belong to the separator, not to the payload.
    while (i < lines.length && lines[i].trim() === '') i++

    const payload: string[] = []
    while (i < lines.length && lines[i].trim() !== '' && !lines[i].includes('-->')) {
      payload.push(lines[i])
      i++
    }

    if (Number.isFinite(start) && Number.isFinite(end)) {
      blocks.push({ start, end, lines: payload })
    }
  }

  return blocks
}

/**
 * Turn one automatic-caption block into word-level pieces.
 *
 * Only the last payload line is read. In the rolling format the earlier lines
 * are the previous cue's text, repeated for the viewer's benefit — reading them
 * is what makes a phrase say itself twice.
 */
function piecesFromTagged(block: RawBlock): Piece[] {
  const line = block.lines[block.lines.length - 1] ?? ''
  const pieces: Piece[] = []

  // Text before the first timestamp tag is spoken from the cue's own start.
  const lead = line.match(/^([^<]+)</)
  if (lead) {
    const text = cleanCueText(lead[1])
    if (text) pieces.push({ start: block.start, end: 0, text })
  }

  const wordRe = /(\d{2}:\d{2}:\d{2}\.\d{3})><c>([^<]*)<\/c>/g
  let m: RegExpExecArray | null
  while ((m = wordRe.exec(line)) !== null) {
    const at = parseVTTTime(m[1])
    const text = cleanCueText(m[2])
    if (text && Number.isFinite(at)) pieces.push({ start: at, end: 0, text })
  }

  if (pieces.length === 0) {
    const text = cleanCueText(line)
    if (text) pieces.push({ start: block.start, end: block.end, text })
    return pieces
  }

  // Each word runs until the next one begins; the last runs to the cue's end.
  for (let k = 0; k < pieces.length; k++) {
    pieces[k].end = k + 1 < pieces.length ? pieces[k + 1].start : block.end
  }
  return pieces
}

/**
 * Where the first clause boundary in `text` is, or -1.
 *
 * The *first*, not the last. Taking the last swallowed every boundary before it,
 * so "Hello there, my friend. How are you" came out as a single cue instead of
 * the three the rule asks for.
 */
export function firstClauseBoundary(text: string): number {
  const re = /[.!?,]/g
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) {
    const ch = m[0]
    const idx = m.index
    const before = text[idx - 1]
    const after = text[idx + 1]

    // "2.5", "3.14" — a decimal point is not the end of anything.
    if (ch === '.' && before && /\d/.test(before) && after && /\d/.test(after)) continue

    if (ch === '.' && before && /[A-Za-z]/.test(before) && after === ' ') {
      const wordBefore = text.slice(0, idx).split(/\s+/).pop() ?? ''
      if (ABBREVIATIONS.test(wordBefore)) continue
    }

    // Mid-word punctuation is not a boundary.
    if (after && after !== ' ') continue

    // A comma is only a boundary when both sides can stand alone. Splitting
    // "Then, how are you" leaves "Then," to be translated with no sentence
    // around it and voiced as a clip half a second long — the opposite of what
    // splitting is for.
    if (ch === ',') {
      const wordsBefore = text.slice(0, idx).trim().split(/\s+/).filter(Boolean).length
      const wordsAfter = text.slice(idx + 1).trim().split(/\s+/).filter(Boolean).length
      if (wordsBefore < MIN_CLAUSE_WORDS || wordsAfter < MIN_CLAUSE_WORDS) continue
    }

    return idx + 1
  }
  return -1
}

/**
 * Gather word pieces into clauses.
 *
 * A clause carries the start of its first word and the end of its last, which
 * is the whole point of keeping per-word timing this far: a clause that claimed
 * the timing of the block it came from would be wrong by however much of that
 * block belonged to the clause before it.
 */
function groupIntoClauses(pieces: Piece[]): CueText[] {
  const out: CueText[] = []
  let buf: Piece[] = []

  const emit = (upTo: number) => {
    // upTo is a character offset into the joined text.
    let consumed = 0
    let cut = -1
    let leftover: Piece | null = null

    for (let k = 0; k < buf.length; k++) {
      const next = consumed + (k > 0 ? 1 : 0) + buf[k].text.length
      if (upTo <= next) {
        cut = k
        const within = upTo - (consumed + (k > 0 ? 1 : 0))
        const rest = buf[k].text.slice(within).trim()
        if (rest) leftover = { start: buf[k].start, end: buf[k].end, text: rest }
        break
      }
      consumed = next
    }
    if (cut < 0) cut = buf.length - 1

    const taken = buf.slice(0, cut + 1)
    const text = stripBrackets(taken.map((p) => p.text).join(' ').slice(0, upTo).trim())
    if (text) {
      out.push({ start: taken[0].start, end: taken[taken.length - 1].end, text })
    }
    buf = leftover ? [leftover, ...buf.slice(cut + 1)] : buf.slice(cut + 1)
  }

  for (const piece of pieces) {
    buf.push(piece)

    // Loop: one block can complete more than one clause.
    for (;;) {
      const joined = buf.map((p) => p.text).join(' ')
      const at = firstClauseBoundary(joined)
      if (at < 0) break
      emit(at)
      if (buf.length === 0) break
    }

    const joined = buf.map((p) => p.text).join(' ')
    if (joined.trim().split(/\s+/).filter(Boolean).length >= FORCE_SPLIT_WORDS) {
      emit(joined.length)
    }
  }

  if (buf.length > 0) {
    const text = stripBrackets(buf.map((p) => p.text).join(' ').trim())
    if (text) out.push({ start: buf[0].start, end: buf[buf.length - 1].end, text })
  }

  return out
}

/**
 * Parse a WebVTT file into the cues the narration speaks.
 *
 * `sourceLang` decides whether clause splitting applies: it is tuned for the
 * punctuation of English and Vietnamese, and a language it does not know is
 * better left in whole cues than cut on rules that do not hold there.
 */
export function parseVTT(raw: string, sourceLang = 'vi'): CueText[] {
  const blocks = readBlocks(raw)
  const isAuto = blocks.some((b) => b.lines.some((l) => l.includes('<c>')))

  const pieces: Piece[] = []
  const manual: CueText[] = []

  for (const block of blocks) {
    // YouTube's ~10ms clean copies of a line already carried by a real cue.
    if (block.end - block.start < SNAPSHOT_MAX_SECONDS) continue
    if (block.lines.length === 0) continue

    const tagged = block.lines[block.lines.length - 1]?.includes('<c>') ?? false

    if (tagged) {
      pieces.push(...piecesFromTagged(block))
      continue
    }

    if (isAuto) {
      // Rolling text without tags: again only the last line is new.
      const text = cleanCueText(block.lines[block.lines.length - 1] ?? '')
      if (text) pieces.push({ start: block.start, end: block.end, text })
      continue
    }

    const text = cleanCueText(block.lines.join(' '))
    if (text) manual.push({ start: block.start, end: block.end, text })
  }

  if (!isAuto) {
    return manual.map((c) => ({ ...c, text: stripBrackets(c.text) })).filter((c) => c.text)
  }

  if (sourceLang !== 'en' && sourceLang !== 'vi') {
    return pieces
      .map((p) => ({ start: p.start, end: p.end, text: stripBrackets(p.text) }))
      .filter((c) => c.text)
  }

  return groupIntoClauses(pieces)
}
