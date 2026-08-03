import { describe, expect, it } from 'vitest'
import { nearestCueIndex } from './narration'
import type { CueText } from './narration-vtt'

const cue = (start: number, end: number, text: string): CueText =>
  ({ start, end, text }) as CueText

describe('nearestCueIndex', () => {
  const cues = [
    cue(0, 2, 'a'),
    cue(10, 12, 'b'),
    cue(20, 22, 'c'),
    cue(30, 32, 'd'),
  ]

  it('starts the pass at the playhead, not at the beginning', () => {
    // Resuming at minute thirty must not translate the opening credits first.
    expect(nearestCueIndex(cues, 19)).toBe(2)
  })

  it('picks the cue in progress', () => {
    expect(nearestCueIndex(cues, 21)).toBe(2)
  })

  it('is zero at the start of the video', () => {
    expect(nearestCueIndex(cues, 0)).toBe(0)
  })

  it('clamps past the end rather than returning -1', () => {
    expect(nearestCueIndex(cues, 999)).toBe(3)
  })

  it('is zero when there are no cues', () => {
    expect(nearestCueIndex([], 5)).toBe(0)
  })
})
