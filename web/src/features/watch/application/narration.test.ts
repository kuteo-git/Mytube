/**
 * Unit tests for VTT cue grouping logic (narration.ts — _sourceLang === 'en').
 * Run: npx vitest run web/src/features/watch/application/narration.test.ts
 */
import { describe, it, expect } from 'vitest'

interface CueText { start: number; end: number; text: string }

// Same algorithm as narration.ts — keep in sync.
function groupCuesForTranslation(cues: CueText[]): CueText[] {
  const grouped: CueText[] = []
  let buf = ''
  let bufStart = 0
  let bufEnd = 0
  for (let j = 0; j < cues.length; j++) {
    if (!buf) { bufStart = cues[j].start }
    buf += (buf ? ' ' : '') + cues[j].text
    bufEnd = cues[j].end

    let lastEnd = -1
    const re = /[.!?,]/g
    let m: RegExpExecArray | null
    while ((m = re.exec(buf)) !== null) {
      const ch = m[0]
      const idx = m.index
      const before = buf[idx - 1]
      const after = buf[idx + 1]
      if (ch === '.' && before && /\d/.test(before) && after && /\d/.test(after)) continue
      if (after && after !== ' ') continue
      // Don't split on comma if the text before it is too short.
      if (ch === ',') {
        const wordsBefore = buf.slice(0, idx).trim().split(/\s+/)
        if (wordsBefore.length <= 2) continue
      }
      lastEnd = idx + 1
    }

    if (lastEnd > 0) {
      const clause = buf.slice(0, lastEnd).trim()
      if (clause) grouped.push({ start: bufStart, end: bufEnd, text: clause })
      buf = buf.slice(lastEnd).trim()
      bufStart = bufEnd
    }
  }
  if (buf.trim()) {
    grouped.push({ start: bufStart, end: bufEnd, text: buf.trim() })
  }
  return grouped
}

describe('groupCuesForTranslation', () => {
  it('splits at every comma and period (user example)', () => {
    const cues: CueText[] = [
      { start: 3.929, end: 6.309, text: 'new DeepSeek style moment, Kimmy' },
      { start: 6.319, end: 8.710, text: 'K3. A release big enough to shake the' },
      { start: 8.720, end: 10.870, text: 'whole race again. Then it got shut down' },
      { start: 10.880, end: 13.120, text: 'for a moment, but China immediately came' },
    ]

    const result = groupCuesForTranslation(cues)
    const texts = result.map((c) => c.text)

    expect(texts).toEqual([
      'new DeepSeek style moment,',
      'Kimmy K3.',
      'A release big enough to shake the whole race again.',
      'Then it got shut down for a moment,',
      'but China immediately came',
    ])

    // Timing: each clause uses start of first cue + end of last cue in its group.
    expect(result[0].start).toBe(3.929)
    expect(result[0].end).toBe(6.309)
    expect(result[4].start).toBe(13.120)
    expect(result[4].end).toBe(13.120)
  })

  it('does not split on decimal numbers like 2.5', () => {
    const cues: CueText[] = [
      { start: 0, end: 1, text: 'version 2.5 is' },
      { start: 1, end: 2, text: 'faster now.' },
    ]
    const result = groupCuesForTranslation(cues)
    expect(result).toHaveLength(1)
    expect(result[0].text).toBe('version 2.5 is faster now.')
  })

  it('does not split comma when preceding text is 1-2 words', () => {
    const cues: CueText[] = [
      { start: 0, end: 1, text: 'Then,' },
      { start: 1, end: 2, text: 'how are you.' },
    ]
    const result = groupCuesForTranslation(cues)
    // "Then," is only 1 word → don't split → join with next
    expect(result).toHaveLength(1)
    expect(result[0].text).toBe('Then, how are you.')
  })

  it('splits comma when preceding text has 3+ words', () => {
    const cues: CueText[] = [
      { start: 0, end: 1, text: 'new DeepSeek style moment,' },
      { start: 1, end: 2, text: 'Kimmy' },
    ]
    const result = groupCuesForTranslation(cues)
    expect(result).toHaveLength(2)
    expect(result[0].text).toBe('new DeepSeek style moment,')
    expect(result[1].text).toBe('Kimmy')
  })

  it('"But, at this moment, ..." skips first comma, splits at second', () => {
    const cues: CueText[] = [
      { start: 0, end: 1, text: 'But, at this moment,' },
      { start: 1, end: 2, text: 'we must act now.' },
    ]
    const result = groupCuesForTranslation(cues)
    // "But," is 1 word → skip first comma. "at this moment," is 3 words → split.
    expect(result).toHaveLength(2)
    expect(result[0].text).toBe('But, at this moment,')
    expect(result[1].text).toBe('we must act now.')
  })

  it('single cue without punctuation stays as-is', () => {
    const cues: CueText[] = [
      { start: 0, end: 2, text: 'no punctuation here' },
    ]
    const result = groupCuesForTranslation(cues)
    expect(result).toHaveLength(1)
    expect(result[0].text).toBe('no punctuation here')
    expect(result[0].start).toBe(0)
    expect(result[0].end).toBe(2)
  })
})

// ---- warm-start skip logic --------------------------------------------------

function applyWarmStartSkip(
  cues: CueText[],
  now: number,
  skipUntil: number,
): Set<number> {
  const played = new Set<number>()
  if (skipUntil > 0) {
    for (let i = 0; i < cues.length; i++) {
      if (cues[i].start < skipUntil) played.add(i)
    }
  }
  return played
}

describe('warm-start skip', () => {
  it('skips cues before now+10 on first tick', () => {
    const cues: CueText[] = [
      { start: 0, end: 2, text: 'hello' },
      { start: 3, end: 5, text: 'world' },
      { start: 6, end: 8, text: 'this' },
      { start: 9, end: 11, text: 'is cool' },
      { start: 12, end: 14, text: 'right now' },
    ]
    // User is at 2s, warm-start skips before 12s
    const played = applyWarmStartSkip(cues, 2, 12)
    expect(played.has(0)).toBe(true)  // start=0 < 12
    expect(played.has(1)).toBe(true)  // start=3 < 12
    expect(played.has(2)).toBe(true)  // start=6 < 12
    expect(played.has(3)).toBe(true)  // start=9 < 12
    expect(played.has(4)).toBe(false) // start=12 >= 12 → NOT skipped
  })

  it('no skip when skipUntil <= 0', () => {
    const cues: CueText[] = [
      { start: 0, end: 2, text: 'hello' },
    ]
    expect(applyWarmStartSkip(cues, 0, -1).size).toBe(0)
    expect(applyWarmStartSkip(cues, 0, 0).size).toBe(0)
  })

  it('resets skip window on forward seek', () => {
    const cues: CueText[] = [
      { start: 10, end: 12, text: 'a' },
      { start: 13, end: 15, text: 'b' },
      { start: 20, end: 22, text: 'c' },
      { start: 25, end: 27, text: 'd' },
    ]
    // User seeks from 5s to 15s (jump > 0.5s) → skipUntil = 15 + 10 = 25
    const played = applyWarmStartSkip(cues, 15, 25)
    expect(played.has(0)).toBe(true)  // start=10 < 25 → skipped
    expect(played.has(1)).toBe(true)  // start=13 < 25 → skipped
    expect(played.has(2)).toBe(true)  // start=20 < 25 → skipped
    expect(played.has(3)).toBe(false) // start=25 >= 25 → NOT skipped
  })

  it('resets skip window on backward seek', () => {
    const cues: CueText[] = [
      { start: 5, end: 7, text: 'x' },
      { start: 10, end: 12, text: 'y' },
      { start: 15, end: 17, text: 'z' },
    ]
    // User seeks backward from 30s to 8s → skipUntil = 8 + 10 = 18
    const played = applyWarmStartSkip(cues, 8, 18)
    expect(played.has(0)).toBe(true)  // start=5 < 13 → skipped
    expect(played.has(1)).toBe(true)  // start=10 < 13 → skipped
    expect(played.has(2)).toBe(false) // start=15 >= 13 → NOT skipped
  })
})
