import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  type CueText,
  cleanCueText,
  firstClauseBoundary,
  parseVTT,
  parseVTTTime,
  stripBrackets,
} from './narration-vtt'

/**
 * The parser, run against real caption files.
 *
 * These are copies of files the ingest pipeline actually downloaded, not
 * examples written to suit the parser. That distinction is the point: the two
 * faults being fixed here — a line occasionally repeating, a line occasionally
 * missing — both come from the shape of YouTube's rolling automatic captions,
 * and neither would appear in a sample invented by whoever wrote the code.
 *
 * The assertions are properties rather than expected output. Nobody can write
 * the correct 400-cue result for an eight-minute video by hand, so a golden file
 * would only ever be a copy of what the parser produced on the day — which is a
 * signature on the bug, not a check of it. Properties hold or they do not.
 *
 * The suite this replaces defined its own copies of parseVTTFile, cleanCueText,
 * stripBrackets and the grouping rule inside the test file and asserted against
 * those. It passed throughout, because the code it tested was not the code that
 * ran.
 */

const dir = join(dirname(fileURLToPath(import.meta.url)), '__fixtures__')
const read = (name: string) => readFileSync(join(dir, name), 'utf8')

const FIXTURES = [
  { name: 'auto-en.vtt', lang: 'en', auto: true },
  { name: 'auto-vi.vtt', lang: 'vi', auto: true },
  { name: 'manual-en.vtt', lang: 'en', auto: false },
  { name: 'manual-vi.vtt', lang: 'vi', auto: false },
] as const

const words = (s: string) => s.trim().split(/\s+/).filter(Boolean)

/**
 * Every timing block that carries speech.
 *
 * YouTube's ~10ms snapshot copies are excluded, and so is anything that leaves
 * nothing behind once sound-effect brackets are removed: a caption file for a
 * music video is page after page of `[Song]`, and having no cue over those is
 * the parser working, not failing.
 */
function speechBlocks(raw: string): { start: number; end: number }[] {
  const lines = raw.replace(/\r\n?/g, '\n').split('\n')
  const out: { start: number; end: number }[] = []

  for (let i = 0; i < lines.length; i++) {
    const arrow = lines[i].indexOf('-->')
    if (arrow < 0) continue

    const start = parseVTTTime(lines[i].slice(0, arrow).trim())
    const end = parseVTTTime(lines[i].slice(arrow + 3).trim().split(/\s/)[0])

    const payload: string[] = []
    for (let k = i + 1; k < lines.length && !lines[k].includes('-->'); k++) {
      payload.push(lines[k])
    }

    if (!Number.isFinite(start) || !Number.isFinite(end)) continue
    if (end - start < 0.1) continue
    if (!stripBrackets(cleanCueText(payload.join(' ')))) continue
    out.push({ start, end })
  }
  return out
}

describe.each(FIXTURES)('$name', ({ name, lang, auto }) => {
  const raw = read(name)
  const cues: CueText[] = parseVTT(raw, lang)

  it('produces cues at all', () => {
    expect(cues.length).toBeGreaterThan(0)
  })

  it('never goes backwards in time', () => {
    for (let i = 1; i < cues.length; i++) {
      expect(cues[i].start).toBeGreaterThanOrEqual(cues[i - 1].start)
    }
  })

  it('gives every cue a positive duration', () => {
    for (const cue of cues) {
      expect(cue.end).toBeGreaterThan(cue.start)
    }
  })

  it('never overlaps the following cue', () => {
    // Two clips scheduled over the same seconds is two voices at once.
    for (let i = 1; i < cues.length; i++) {
      expect(cues[i - 1].end).toBeLessThanOrEqual(cues[i].start + 0.001)
    }
  })

  it('does not repeat the end of one cue at the start of the next', () => {
    // The rolling format carries the previous line forward. Reading that
    // carried text is what made a phrase say itself twice.
    for (let i = 1; i < cues.length; i++) {
      const prev = words(cues[i - 1].text)
      const cur = words(cues[i].text)
      if (prev.length < 3 || cur.length < 3) continue
      const tail = prev.slice(-3).join(' ').toLowerCase()
      const head = cur.slice(0, 3).join(' ').toLowerCase()
      expect(head, `cue ${i} repeats the tail of cue ${i - 1}`).not.toBe(tail)
    }
  })

  it('leaves no stretch of the file unspoken', () => {
    // Every real timing block should be covered by some cue. A block with
    // nothing over it is a line that was dropped — which is what an unguarded
    // step over a timing line used to do.
    const blocks = speechBlocks(raw)
    const uncovered = blocks.filter(
      (b) => !cues.some((c) => c.start < b.end && c.end > b.start),
    )
    expect(uncovered, `${uncovered.length} of ${blocks.length} speech blocks unspoken`).toEqual([])
  })

  it('does not invent words', () => {
    const inFile = words(raw.replace(/<[^>]*>/g, ' ')).length
    const inCues = cues.reduce((n, c) => n + words(c.text).length, 0)
    expect(inCues).toBeLessThanOrEqual(inFile)
  })

  it('leaves no cue empty', () => {
    for (const cue of cues) expect(cue.text.trim()).not.toBe('')
  })

  if (auto) {
    it('breaks at clause boundaries', () => {
      // Every cue but the last should end at punctuation, unless it hit the
      // length limit that exists for captions with no punctuation at all.
      const ragged = cues
        .slice(0, -1)
        .filter((c) => !/[.!?,]$/.test(c.text) && words(c.text).length < 30)
      expect(ragged.length / cues.length).toBeLessThan(0.15)
    })

    it('never splits off a fragment too short to translate', () => {
      // "Then," on its own has no sentence around it for a translator to use,
      // and becomes a clip half a second long.
      const fragments = cues.filter((c) => /,$/.test(c.text) && words(c.text).length < 3)
      expect(fragments).toEqual([])
    })
  }
})

describe('firstClauseBoundary', () => {
  it('takes the first boundary, not the last', () => {
    // Taking the last swallowed everything before it into one cue.
    const text = 'Hello there my friend. How are you'
    expect(firstClauseBoundary(text)).toBe('Hello there my friend.'.length)
  })

  it('ignores a decimal point', () => {
    expect(firstClauseBoundary('it costs 2.5 dollars')).toBe(-1)
  })

  it('ignores common abbreviations', () => {
    expect(firstClauseBoundary('ask Dr. Smith about it')).toBe(-1)
  })

  it('ignores a comma with too little on one side', () => {
    expect(firstClauseBoundary('Then, how are you')).toBe(-1)
  })

  it('takes a comma with enough on both sides', () => {
    const text = 'wait a moment, and then we go'
    expect(firstClauseBoundary(text)).toBe('wait a moment,'.length)
  })

  it('finds a boundary at the very end', () => {
    expect(firstClauseBoundary('all done.')).toBe('all done.'.length)
  })
})

describe('parseVTTTime', () => {
  it('reads hours, minutes and fractional seconds', () => {
    expect(parseVTTTime('00:01:23.456')).toBeCloseTo(83.456)
    expect(parseVTTTime('01:00:00.000')).toBe(3600)
  })

  it('rejects anything that is not a timestamp', () => {
    expect(Number.isNaN(parseVTTTime('nonsense'))).toBe(true)
  })
})

describe('shapes that broke the old parser', () => {
  it('keeps a cue whose payload runs straight into the next timing line', () => {
    // No blank separator. The old code stepped over the following timing line
    // unconditionally after reading a payload, losing the cue it introduced.
    const raw = [
      'WEBVTT',
      '',
      '00:00:01.000 --> 00:00:02.000',
      'first line',
      '00:00:03.000 --> 00:00:04.000',
      'second line',
      '',
    ].join('\n')

    const cues = parseVTT(raw, 'en')
    expect(cues.map((c) => c.text)).toEqual(['first line', 'second line'])
  })

  it('reads only the new line of a rolling automatic cue', () => {
    const raw = [
      'WEBVTT',
      '',
      '00:00:00.000 --> 00:00:02.000',
      ' ',
      'hello<00:00:00.500><c> there</c>',
      '',
      '00:00:02.000 --> 00:00:04.000',
      'hello there',
      'friend<00:00:02.500><c> of</c><00:00:03.000><c> mine.</c>',
      '',
    ].join('\n')

    const cues = parseVTT(raw, 'en')
    const all = cues.map((c) => c.text).join(' ')
    expect(all).toBe('hello there friend of mine.')
    // "hello there" appears once, not twice.
    expect(all.match(/hello/g)).toHaveLength(1)
  })

  it('gives a trailing clause the time of its own words', () => {
    // The remainder after a split used to inherit the end of the whole buffer,
    // claiming a moment later than the words it contains.
    const raw = [
      'WEBVTT',
      '',
      '00:00:00.000 --> 00:00:06.000',
      'one<00:00:01.000><c> two.</c><00:00:02.000><c> three</c><00:00:03.000><c> four</c>',
      '',
    ].join('\n')

    const cues = parseVTT(raw, 'en')
    expect(cues.length).toBe(2)
    expect(cues[0].text).toBe('one two.')
    expect(cues[1].text).toBe('three four')
    // The second clause starts when "three" is spoken, not after "four".
    expect(cues[1].start).toBeCloseTo(2)
  })
})
