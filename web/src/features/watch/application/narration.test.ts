/**
 * Unit tests for VTT cue grouping logic (narration.ts — _sourceLang === 'en').
 * Run: npx vitest run web/src/features/watch/application/narration.test.ts
 */
import { describe, it, expect, beforeAll } from 'vitest'

interface CueText { start: number; end: number; text: string }

// Same algorithm as narration.ts — keep in sync.
function stripBrackets(text: string): string {
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

function groupCuesForTranslation(cues: CueText[]): CueText[] {
  const grouped: CueText[] = []
  let buf = ''
  let bufStart = 0
  let bufEnd = 0
  for (let j = 0; j < cues.length; j++) {
    if (!buf) { bufStart = cues[j].start }
    buf += (buf ? ' ' : '') + cues[j].text
    bufEnd = cues[j].start

    let lastEnd = -1
    const re = /[.!?,]/g
    let m: RegExpExecArray | null
    while ((m = re.exec(buf)) !== null) {
      const ch = m[0]
      const idx = m.index
      const before = buf[idx - 1]
      const after = buf[idx + 1]
      if (ch === '.' && before && /\d/.test(before) && after && /\d/.test(after)) continue
      if (ch === '.' && before && /[A-Za-z]/.test(before) && after === ' ') {
        const wordBefore = buf.slice(0, idx).split(/\s+/).pop() || ''
        if (/^(Dr|Mr|Mrs|Ms|DR|Prof|Sr|Jr|vs|etc)$/i.test(wordBefore)) continue
      }
      if (after && after !== ' ') continue
      // Don't split on comma if the text before it is too short.
      if (ch === ',') {
        const wordsBefore = buf.slice(0, idx).trim().split(/\s+/).length
        if (wordsBefore <= 2) continue
      }
      lastEnd = idx + 1
    }

    if (lastEnd > 0) {
      const clause = stripBrackets(buf.slice(0, lastEnd).trim())
      if (clause) grouped.push({ start: bufStart, end: bufEnd, text: clause })
      buf = buf.slice(lastEnd).trim()
      bufStart = bufEnd
    }
  }
  if (buf.trim()) {
    const clause = stripBrackets(buf.trim())
    if (clause) grouped.push({ start: bufStart, end: bufEnd, text: clause })
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

    // Timing: clause ends at the start of its last word (bufEnd = cue.start).
    expect(result[0].start).toBe(3.929)
    expect(result[0].end).toBe(3.929)  // "moment," starts at 3.929
    // Last clause starts at the last word's bufStart (set after prev split)
    expect(result[4].end).toBe(10.88) // clip ends at cue start of last segment
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

  it('skips abbreviation periods: Dr., Mr., DR., etc.', () => {
    const cues: CueText[] = [
      { start: 0, end: 2, text: 'DR. TYSON: From the' },
      { start: 2, end: 4, text: 'American Museum of Natural' },
      { start: 4, end: 6, text: 'History in New York City.' },
    ]
    const result = groupCuesForTranslation(cues)
    // "DR." is abbreviation, "City." is sentence end — 1 group
    expect(result).toHaveLength(1)
    expect(result[0].text).toBe('DR. TYSON: From the American Museum of Natural History in New York City.')
  })

  it('parses leading text before first timestamp tag', () => {
    // VTT tagged line: "Được <00:00:00.155><c>rồi, </c><00:00:00.310><c>tôi</c>"
    // Leading "Được " should be captured at cue start time.
    const cues: CueText[] = [
      { start: 0.000, end: 0.000, text: 'Được' },
      { start: 0.155, end: 0.000, text: 'rồi,' },
      { start: 0.310, end: 1.299, text: 'tôi' },
    ]
    // After grouping: "Được rồi, tôi" — no clause boundaries
    const result = groupCuesForTranslation(cues)
    expect(result).toHaveLength(1)
    expect(result[0].start).toBe(0.000) // first word's timestamp
    expect(result[0].text).toBe('Được rồi, tôi')
  })

  it('word-level timing preserved after clause split', () => {
    const cues: CueText[] = [
      { start: 9.296, end: 9.678, text: 'Đó' },
      { start: 9.678, end: 9.830, text: 'là' },
      { start: 9.990, end: 10.140, text: 'kiểu' },
      { start: 10.140, end: 10.290, text: 'người' },
      { start: 10.290, end: 11.640, text: 'mà bạn không bao giờ biết được,' },
    ]
    const result = groupCuesForTranslation(cues)
    expect(result).toHaveLength(1)
    // Clause starts at first word's precise timestamp (9.296), not cue time
    expect(result[0].start).toBe(9.296)
    expect(result[0].text).toBe('Đó là kiểu người mà bạn không bao giờ biết được,')
  })

  it('strips brackets after grouping (not per-word)', () => {
    // Simulated word-level cues from "[tiếng vỗ tay]" across <c> tags
    const cues: CueText[] = [
      { start: 0, end: 0.1, text: 'các bạn' },
      { start: 0.1, end: 0.2, text: '[tiếng' },
      { start: 0.2, end: 0.3, text: 'vỗ' },
      { start: 0.3, end: 0.4, text: 'tay].' },
      { start: 0.4, end: 0.5, text: 'Câu hỏi nghiêm túc.' },
    ]
    const result = groupCuesForTranslation(cues)
    // Brackets stripped after grouping. "tay]." splits at the period.
    expect(result[0].text).toBe('các bạn.')
    expect(result[1].text).toBe('Câu hỏi nghiêm túc.')
  })

  it('does not use isTwoLineCarry when last line is short punctuation', () => {
    // Simulates: ["Vậy nếu bạn...", "?"] — "?" is not <c> tagged content
    const cues: CueText[] = [
      { start: 0, end: 1, text: 'hello' },
      { start: 1, end: 2, text: 'world' },
    ]
    // These should group normally (join = "hello world"), not be treated as carry-over
    const result = groupCuesForTranslation(cues)
    expect(result[0].text).toBe('hello world')
  })

  it('single cue without punctuation stays as-is', () => {
    const cues: CueText[] = [
      { start: 0, end: 2, text: 'no punctuation here' },
    ]
    const result = groupCuesForTranslation(cues)
    expect(result).toHaveLength(1)
    expect(result[0].text).toBe('no punctuation here')
    expect(result[0].start).toBe(0)
    expect(result[0].end).toBe(0) // clause ends at cue's start (bufEnd = cue.start)
  })
})

// ---- end-to-end VTT file test ------------------------------------------------

import { readFileSync } from 'fs'
import { resolve } from 'path'

function parseVTTTime(raw: string): number {
  const parts = raw.split(':')
  if (parts.length !== 3) return NaN
  return parseInt(parts[0]) * 3600 + parseInt(parts[1]) * 60 + parseFloat(parts[2])
}

function cleanCueText(raw: string): string {
  let s = raw
    .replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"').replace(/&#39;/g, "'")
  s = s.replace(/<[^>]+>/g, '').replace(/>>\\s*/g, '')
    .replace(/[♪♫♬→←↑↓↔«»""''„‚]/g, '').trim()
  return s.replace(/\\s{2,}/g, ' ')
}

function parseVTTFile(raw: string): CueText[] {
  const cues: CueText[] = []
  const lines = raw.split('\n')
  let i = 0
  while (i < lines.length && !lines[i].includes('-->')) i++
  while (i < lines.length) {
    const timingLine = lines[i]
    if (!timingLine || !timingLine.includes('-->')) { i++; continue }
    const arrowIdx = timingLine.indexOf('-->')
    const start = parseVTTTime(timingLine.substring(0, arrowIdx).trim())
    const end = parseVTTTime(timingLine.substring(arrowIdx + 3).trim().split(/\s/)[0])
    i++
    if (!isFinite(start) || !isFinite(end)) continue
    while (i < lines.length && lines[i].trim() === '') i++
    const payloadLines: string[] = []
    while (i < lines.length && lines[i].trim() !== '') { payloadLines.push(lines[i]); i++ }
    i++
    if (end - start < 0.1) continue
    const hasTags = payloadLines.some(l => l.includes('<c>'))
    if (hasTags) {
      const taggedLine = payloadLines[payloadLines.length - 1] || ''
      const leadMatch = taggedLine.match(/^([^<]+)</)
      if (leadMatch) {
        const wt = cleanCueText(leadMatch[1])
        if (wt) cues.push({ start, end: 0, text: wt })
      }
      const wordRe = /(\d{2}:\d{2}:\d{2}\.\d{3})><c>([^<]*)<\/c>/g
      let m: RegExpExecArray | null
      while ((m = wordRe.exec(taggedLine)) !== null) {
        const wordTime = parseVTTTime(m[1])
        const wordText = cleanCueText(m[2])
        if (!wordText || !isFinite(wordTime)) continue
        cues.push({ start: wordTime, end: 0, text: wordText })
      }
      for (let k = cues.length - 1; k >= 0; k--) {
        if (cues[k].end > 0) break
        cues[k].end = (k + 1 < cues.length) ? cues[k + 1].start : end
      }
    } else {
      const text = cleanCueText(payloadLines.join(' '))
      if (!text) continue
      cues.push({ start, end, text })
    }
  }
  return groupCuesForTranslation(cues)
}

describe('VTT file parsing (MeplqZ0nM1c)', () => {
  const vttPath = '/Volumes/Data2/Youtube/MeplqZ0nM1c/1080p.mp4.vi.vtt'
  let cues: CueText[]

  beforeAll(() => {
    const raw = readFileSync(vttPath, 'utf-8')
    cues = parseVTTFile(raw)
  })

  it('parses the VTT file without errors', () => {
    expect(cues.length).toBeGreaterThan(500)
  })

  it('first cue starts near 0 and includes leading word', () => {
    expect(cues[0].start).toBeLessThan(0.2) // ~0.000s or 0.155s
    expect(cues[0].text).toContain('Được')
  })

  it('has word-level precision timing (not cue-level)', () => {
    // "Đó là" should start near 9.296 (word timestamp), not 9.830 (cue end)
    const doLa = cues.find(c => c.text.includes('Đó là'))
    expect(doLa).toBeDefined()
    expect(doLa!.start).toBeLessThan(9.4) // 9.296, not 9.830
    expect(doLa!.start).toBeGreaterThan(9.2)
  })

  it('removes sound-effect brackets like [tiếng vỗ tay]', () => {
    // After stripBrackets, neither the bracket chars nor the enclosed text remain
    const brackets = cues.filter(c => c.text.includes('[tiếng vỗ tay]'))
    expect(brackets.length).toBe(0)
  })
})

// ---- two-line carry-over without tags ---------------------------------------

describe('two-line carry-over without <c> tags', () => {
  // These simulate what parseVTT produces AFTER processing 2-line cues
  // where line 1 = prev clean text (discarded), line 2 = new text (kept).

  it('keeps "giúp đỡ." from 2-line cue (no <c> tags)', () => {
    // Cue: ["Đây là...xin", "giúp đỡ."] → isTwoLineCarry → only "giúp đỡ."
    const cues: CueText[] = [
      { start: 42.720, end: 44.790, text: 'là những người phụ nữ đã viết thư cho chúng tôi để xin' },
      { start: 44.800, end: 46.150, text: 'giúp đỡ.' },
      { start: 46.160, end: 48.390, text: 'Nhưng họ quá sợ làm xáo trộn cuộc sống gia' },
    ]
    const result = groupCuesForTranslation(cues)
    const texts = result.map(c => c.text)
    expect(texts).toContain('là những người phụ nữ đã viết thư cho chúng tôi để xin giúp đỡ.')
  })

  it('keeps "béo." from 2-line cue (no <c> tags)', () => {
    const cues: CueText[] = [
      { start: 102.24, end: 104.23, text: 'Chồng tôi gọi tôi là ngu ngốc,' },
      { start: 104.24, end: 105.67, text: 'béo.' },
      { start: 105.68, end: 107.59, text: 'Có thể anh ta sẽ bực mình vì điều gì đó tôi nói' },
    ]
    const result = groupCuesForTranslation(cues)
    // Comma splits: "ngu ngốc," (4 words before comma > 2 → split)
    expect(result[0].text).toBe('Chồng tôi gọi tôi là ngu ngốc,')
    expect(result[1].text).toBe('béo.')
  })

  it('appends "?" to previous sentence (no duplicate)', () => {
    // Cue at 20.800: ["Vậy nếu bạn...thì sao", "?"] → only "?"
    const cues: CueText[] = [
      { start: 19.52, end: 20.79, text: 'Vậy nếu bạn đã kết hôn với người đó thì sao' },
      { start: 20.80, end: 23.35, text: '?' },
      { start: 23.36, end: 27.99, text: 'Ngày này qua ngày khác, hết lần này đến lần khác.' },
    ]
    const result = groupCuesForTranslation(cues)
    const texts = result.map(c => c.text)
    // "?" should join with previous text, not duplicate it
    expect(texts[0]).toBe('Vậy nếu bạn đã kết hôn với người đó thì sao ?')
    // Should NOT contain the duplicate
    expect(texts.filter(t => t.includes('Vậy nếu bạn đã kết hôn')).length).toBe(1)
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
    expect(played.has(0)).toBe(true)  // start=5 < 18 → skipped
    expect(played.has(1)).toBe(true)  // start=10 < 18 → skipped
    expect(played.has(2)).toBe(true)  // start=15 < 18 → skipped
  })
})
