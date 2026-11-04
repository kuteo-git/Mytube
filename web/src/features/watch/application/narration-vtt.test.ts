import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  type CueText,
  cleanCueText,
  firstClauseBoundary,
  lacksPunctuation,
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

  it('ignores a comma that closes too short a clause', () => {
    // Three words is the same fault one word is, and measuring said so: this
    // clause is 0.80s of speech. MIN_CLAUSE_BEFORE is five.
    expect(firstClauseBoundary('wait a moment, and then we go')).toBe(-1)
  })

  it('takes a comma with enough on both sides', () => {
    const text = 'wait here for a moment, and then we go'
    expect(firstClauseBoundary(text)).toBe('wait here for a moment,'.length)
  })

  it('ignores a comma inside an enumeration', () => {
    // What follows this comma is one word, so the clause it opens cannot stand
    // alone however long the rest of the sentence is.
    const text = 'if you have got the AirPods 4, 3, or 2, here is how they compare'
    expect(firstClauseBoundary(text)).toBe('if you have got the AirPods 4, 3, or 2,'.length)
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

describe('a cue with no speech in it', () => {
  it('does not lend its timestamp to the line after it', () => {
    // Copied from 8OPCWER_68U: "[âm nhạc]" runs 6.55–11.43 and the greeting
    // begins at 11.44, so the clause the two formed spoke five seconds early.
    const raw = [
      'WEBVTT',
      '',
      '00:00:06.550 --> 00:00:11.430 align:start position:0%',
      ' ',
      '[âm nhạc]',
      '',
      '00:00:11.440 --> 00:00:12.709 align:start position:0%',
      ' ',
      'Họ<00:00:11.639><c> đang</c><00:00:11.719><c> kính</c><00:00:11.840><c> chào</c><00:00:11.960><c> quý</c><00:00:12.120><c> vị.</c>',
      '',
    ].join('\n')

    const cues = parseVTT(raw, 'vi')
    expect(cues.length).toBeGreaterThan(0)
    expect(cues[0].start).toBeCloseTo(11.44)
  })
})

/**
 * The reported fault: cues cut mid-phrase instead of at punctuation.
 *
 * Every sentence here came off one measured video (kXVt4atqMv8) and each one
 * broke in a different place, so they are kept whole rather than reduced to the
 * rule each happens to exercise. They are fed through `parseVTT` as automatic
 * captions one word at a time, which is how the rolling format really arrives —
 * a boundary the parser only finds once the whole sentence is present would
 * pass a test on the joined text and still cut the video wrongly.
 */
describe('clauses break at punctuation, not mid-phrase', () => {
  /** One sentence as a rolling automatic cue, one word per timestamp. */
  const rolling = (sentence: string): string => {
    const words = sentence.split(/\s+/).filter(Boolean)
    const at = (i: number) => {
      const t = i * 0.5
      const mm = String(Math.floor(t / 60)).padStart(2, '0')
      const ss = (t % 60).toFixed(3).padStart(6, '0')
      return `00:${mm}:${ss}`
    }
    const tagged = words
      .map((w, i) => (i === 0 ? w : `<${at(i)}><c> ${w}</c>`))
      .join('')
    return ['WEBVTT', '', `${at(0)} --> ${at(words.length)}`, ' ', tagged, ''].join('\n')
  }

  const cases: Array<{ name: string; sentence: string; want: string[] }> = [
    {
      // Split at the list comma this left "3, or 2." alone, 0.72s long.
      name: 'an enumeration is not split between its items',
      sentence: "If you've already got the AirPods 4, 3, or 2, well, here's how they all compare.",
      want: [
        "If you've already got the AirPods 4, 3, or 2, well,",
        "here's how they all compare.",
      ],
    },
    {
      // "The AirPods 3," was a cue of its own, 0.80s long.
      name: 'an appositive does not end a clause',
      sentence: 'The AirPods 3, however, were about a 6 out of 10.',
      want: ['The AirPods 3, however, were about a 6 out of 10.'],
    },
    {
      // The rule the comma split exists for, and it still holds.
      name: 'a comma between two full clauses still splits',
      sentence:
        "and it's also a bit more snug to help with passive noise cancellation, which in turn helps with active noise cancellation.",
      want: [
        "and it's also a bit more snug to help with passive noise cancellation,",
        'which in turn helps with active noise cancellation.',
      ],
    },
    {
      // The case the "first, not last" rule was written for.
      name: 'a full stop still ends a clause',
      sentence: 'Hello there, my friend. How are you',
      want: ['Hello there, my friend.', 'How are you'],
    },
    {
      // A blind cut must not beat a full stop to it. No punctuation until the
      // last two words; at 30 this came out as "...maybe slightly" plus
      // "below those." — a cue 0.72s long that says nothing on its own.
      name: 'a blind cut does not beat a nearby full stop',
      sentence:
        "I would say that they're even better than the original AirPods Pros were and maybe even on the same level as the AirPods Pro 2os or maybe okay maybe slightly below those.",
      want: [
        "I would say that they're even better than the original AirPods Pros were and maybe even on the same level as the AirPods Pro 2os or maybe okay maybe slightly below those.",
      ],
    },
  ]

  for (const c of cases) {
    it(c.name, () => {
      expect(parseVTT(rolling(c.sentence), 'en').map((cue) => cue.text)).toEqual(c.want)
    })
  }
})

/**
 * Automatic captions with no punctuation at all.
 *
 * `auto-en-bare.vtt` is the first minute of t_YyrMV7SuE, copied verbatim: old
 * ASR, lowercase, not one of . , ? ! in 3,577 words. With nothing to cut on,
 * the 40-word ceiling was the only boundary, so 89 of its 90 cues were exactly
 * 40 words and "every new idea that comes out of there they adopted
 * immediately" was voiced as two halves. The expected cues below were worked
 * out from this file's word timestamps (a pause is >= 0.7s between the starts
 * of two words, with five words already gathered), not from the parser.
 *
 * `auto-vi-bare.vtt` is the first 14 seconds of _f5QLW5tphE, likewise.
 */
describe('automatic captions with no punctuation', () => {
  const bareEn = read('auto-en-bare.vtt')
  const near = (a: number, b: number) => Math.abs(a - b) < 1e-6

  /** The blocks from the one whose timing line contains `from`, up to the one containing `to`. */
  const between = (raw: string, from: string, to: string): string => {
    const kept: string[] = []
    let on = false
    for (const line of raw.split('\n')) {
      if (line.includes('-->')) {
        if (line.includes(from)) on = true
        if (line.includes(to)) on = false
      }
      if (on) kept.push(line)
    }
    return `WEBVTT\n\n${kept.join('\n')}\n`
  }

  /** Every timestamp moved later by whole seconds. */
  const shift = (raw: string, seconds: number): string =>
    raw.replace(/\d{2}:\d{2}:\d{2}\.\d{3}/g, (ts) => {
      const ms = Math.round(parseVTTTime(ts) * 1000) + seconds * 1000
      const p = (n: number, w: number) => String(n).padStart(w, '0')
      return `${p(Math.floor(ms / 3600000), 2)}:${p(Math.floor(ms / 60000) % 60, 2)}:${p(Math.floor(ms / 1000) % 60, 2)}.${p(ms % 1000, 3)}`
    })

  const expectCues = (got: CueText[], want: Array<[number, string]>) => {
    expect(got.length).toBeGreaterThanOrEqual(want.length)
    want.forEach(([start, text], i) => {
      expect(near(got[i].start, start), `cue ${i} starts at ${got[i].start}, want ${start}`).toBe(true)
      expect(got[i].text).toBe(text)
    })
  }

  const FIRST_SEVEN: Array<[number, string]> = [
    [0.16, "some of you know what i'm talking about can you turn a compliment into an argument that's what i want to know"],
    [8.08, 'i i live in los angeles california'],
    [10.08, 'and uh every new idea that comes out of there they adopted immediately'],
    [13.679, "and i'm a little more conservative"],
    [15.36, "like the new one is any man that says they're a woman immediately you must address him as a woman and i'm progressive i'm like all right but they should have to pass a test first"],
    [24.0, "because women have skills men don't have you can't just say i'm a woman do something a woman can do then i'll believe you"],
    [30.0, 'right can you break into my iphone and the time i go to the bathroom and come back to the dinner table'],
  ]

  it('ends a clause at a pause in speech', () => {
    const cues = parseVTT(bareEn, 'en')
    expectCues(cues, [...FIRST_SEVEN, [38.239, 'just something basic can you be totally starving but have no idea what you want to']])

    // A clause ends where the last word of it ends: "california" runs until "and".
    expect(near(cues[1].end, 10.08)).toBe(true)

    for (const c of cues) {
      expect(words(c.text).length, `cue at ${c.start} is a blind 40-word cut: ${c.text}`).toBeLessThan(40)
    }
  })

  it('starts the cue after applause at the next word, not at the applause', () => {
    // "[Applause]" runs from 6.29 to 8.07 and "i" is spoken at 8.08.
    const cues = parseVTT(bareEn, 'en')
    expect(cues[0].text.endsWith("that's what i want to know")).toBe(true)
    expect(cues[0].text + cues[1].text).not.toMatch(/applause/i)
    expect(near(cues[1].start, 8.08)).toBe(true)
  })

  it('ends a clause at a pause only where the track has no punctuation', () => {
    // The same twenty words, once bare and once with a single full stop on the
    // last word. A punctuated track has long comma-less stretches that are not
    // pauses between clauses, so the 0.8s between "california" and "and" ends a
    // clause only in the bare track.
    const bare = between(bareEn, '00:00:08.080', '00:00:13.679')
    const punctuated = bare.replace('immediately</c>', 'immediately.</c>')
    expect(punctuated).not.toBe(bare)

    expectCues(parseVTT(bare, 'en'), [
      [8.08, 'i i live in los angeles california'],
      [10.08, 'and uh every new idea that comes out of there they adopted immediately'],
    ])
    expect(parseVTT(punctuated, 'en').map((c) => [c.start, c.text])).toEqual([
      [8.08, 'i i live in los angeles california and uh every new idea that comes out of there they adopted immediately.'],
    ])
  })

  it('still cuts at 40 words where there is no pause', () => {
    // Words from the fixture, said at an even 0.25s.
    const ws = (
      "like the new one is any man that says they're a woman immediately " +
      'you must address him as a woman and i\'m progressive i\'m like all right but ' +
      "they should have to pass a test first because women have skills men don't " +
      "have you can't"
    ).split(' ')
    expect(ws).toHaveLength(45)
    const at = (t: number) => `00:00:${t.toFixed(3).padStart(6, '0')}`
    const line = ws.map((w, i) => (i === 0 ? w : `<${at(i * 0.25)}><c> ${w}</c>`)).join('')
    const raw = `WEBVTT\n\n00:00:00.000 --> 00:00:11.500\n \n${line}\n`

    expect(parseVTT(raw, 'en').map((c) => c.text)).toEqual([ws.slice(0, 40).join(' '), ws.slice(40).join(' ')])
  })

  it('needs five words before a pause ends a clause', () => {
    // "and uh every new idea" with the pause after the fourth word, then after
    // the fifth. Times are the fixture's, 10.08 onward, with one gap widened.
    const cue = (line: string) =>
      parseVTT(`WEBVTT\n\n00:00:10.080 --> 00:00:13.000\n \n${line}\n`, 'en').map((c) => c.text)

    expect(
      cue('and<00:00:10.320><c> uh</c><00:00:10.719><c> every</c><00:00:11.120><c> new</c><00:00:12.000><c> idea</c><00:00:12.240><c> that</c>'),
    ).toEqual(['and uh every new idea that'])
    expect(
      cue('and<00:00:10.320><c> uh</c><00:00:10.719><c> every</c><00:00:11.120><c> new</c><00:00:11.360><c> idea</c><00:00:12.240><c> that</c>'),
    ).toEqual(['and uh every new idea', 'that'])
  })

  it('is not made punctuated by one stray mark in 338 words', () => {
    // Three bare tracks in the library carry one. The threshold is a rate
    // (fewer than one mark per hundred words), not "none at all".
    const first = bareEn.replace('<c> table</c>', '<c> table.</c>')
    const raw = `${first}\n${shift(bareEn, 60)}`
    const cues = parseVTT(raw, 'en')

    expectCues(cues, [
      ...FIRST_SEVEN.slice(0, 6),
      [30.0, 'right can you break into my iphone and the time i go to the bathroom and come back to the dinner table.'],
    ])

    const byStart = new Map(cues.map((c) => [Math.round(c.start * 1000), c.text]))
    expect(byStart.get(68080)).toBe('i i live in los angeles california')
    expect(byStart.get(70080)).toBe('and uh every new idea that comes out of there they adopted immediately')
    expect(byStart.get(73679)).toBe("and i'm a little more conservative")
  })

  it('splits Vietnamese on pauses too', () => {
    // 39 words in the first 13s: below the 40-word ceiling, so before this it
    // was one cue.
    const cues = parseVTT(read('auto-vi-bare.vtt'), 'vi')
    expect(cues).toHaveLength(3)
    expectCues(cues, [
      [0.32, 'chỉ có lời hứa thôi Mà lỡ dở ra tôi còn buồn như vậy con langan còn buồn như vậy Huân'],
      [7.6, 'chi hoàn cảnh của cha con ông phủ Trần Còn khổ sở đến mức nào nữa'],
      [12.44, 'chị Chị'],
    ])
  })

  it('leaves other languages in whole pieces', () => {
    const cues = parseVTT(bareEn, 'ja')
    expect(cues).toHaveLength(169)
    expect(cues[0].text).toBe('some')
    expect(near(cues[0].start, 0.16)).toBe(true)
  })

  it('measures a pause after a line with no word timing from the line\'s end', () => {
    // A rolling cue without tags is one piece holding several words. Its start
    // is the line's, so measured start to start the pause before the next line
    // would be the whole line's duration.
    const raw = (nextStart: string) =>
      [
        'WEBVTT',
        '',
        '00:00:00.000 --> 00:00:01.000',
        ' ',
        'zero<00:00:00.500><c> a</c>',
        '',
        '00:00:01.000 --> 00:00:03.000',
        'zero a',
        'one two three four five six',
        '',
        `${nextStart} --> 00:00:09.000`,
        'one two three four five six',
        'seven',
        '',
      ].join('\n')

    expect(parseVTT(raw('00:00:03.500'), 'en').map((c) => c.text)).toEqual(['zero a one two three four five six seven'])
    expect(parseVTT(raw('00:00:04.000'), 'en').map((c) => c.text)).toEqual(['zero a one two three four five six', 'seven'])
  })
})

describe('lacksPunctuation', () => {
  const pieces = (n: number, marked = '') =>
    Array.from({ length: n }, (_, i) => ({ start: i, end: i + 1, text: i === 0 && marked ? marked : 'word' }))

  it('is a rate, not an absence', () => {
    expect(lacksPunctuation(pieces(300))).toBe(true)
    expect(lacksPunctuation(pieces(300, 'end.'))).toBe(true)
    expect(lacksPunctuation(pieces(101, 'end.'))).toBe(true)
    expect(lacksPunctuation(pieces(100, 'end.'))).toBe(false)
  })

  it('counts commas, question marks and exclamation marks', () => {
    expect(lacksPunctuation(pieces(50, 'a,'))).toBe(false)
    expect(lacksPunctuation([...pieces(150, 'why?'), { start: 0, end: 1, text: 'no!' }])).toBe(false)
  })
})
