import { describe, expect, it } from 'vitest'
import { formatVTTTime, toVTT } from './narration-vtt-write'

describe('formatVTTTime', () => {
  it('writes hours, minutes, seconds and milliseconds', () => {
    expect(formatVTTTime(0)).toBe('00:00:00.000')
    expect(formatVTTTime(2.72)).toBe('00:00:02.720')
    expect(formatVTTTime(61.5)).toBe('00:01:01.500')
    expect(formatVTTTime(3723.004)).toBe('01:02:03.004')
  })

  it('does not emit a negative stamp', () => {
    // A cue that starts fractionally before zero would make the file
    // unparseable rather than merely early.
    expect(formatVTTTime(-0.5)).toBe('00:00:00.000')
  })
})

describe('toVTT', () => {
  const cues = [
    { start: 0, end: 2, text: 'a' },
    { start: 10, end: 12.5, text: 'b' },
  ]

  it('writes a header and one block per translated cue', () => {
    const out = toVTT(cues, new Map([['a', 'một'], ['b', 'hai']]))
    expect(out).toBe(
      'WEBVTT\n\n' +
        '00:00:00.000 --> 00:00:02.000\nmột\n\n' +
        '00:00:10.000 --> 00:00:12.500\nhai\n',
    )
  })

  it('omits cues with no translation rather than falling back to English', () => {
    // A file half in English, presented as a Vietnamese track, is worse than a
    // shorter file that is entirely Vietnamese.
    const out = toVTT(cues, new Map([['a', 'một']]))
    expect(out).toContain('một')
    expect(out).not.toContain('b')
  })

  it('returns an empty string when nothing is translated', () => {
    // The caller uses this to decide there is no file worth writing.
    expect(toVTT(cues, new Map())).toBe('')
  })

  it('escapes a line that would look like a cue separator', () => {
    const out = toVTT([{ start: 0, end: 1, text: 'x' }], new Map([['x', 'a\n\nb']]))
    expect(out).toContain('a b')
  })
})
