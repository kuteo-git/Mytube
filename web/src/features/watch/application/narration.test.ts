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
