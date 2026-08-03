import { describe, expect, it } from 'vitest'
import { currentCueText } from './narration'
import type { CueText } from './narration-vtt'

const cue = (start: number, end: number, text: string): CueText =>
  ({ start, end, text }) as CueText

describe('currentCueText', () => {
  const cues = [cue(0, 2, 'a'), cue(10, 12, 'b')]

  it('returns the cue covering the moment', () => {
    expect(currentCueText(cues, 11)).toBe('b')
  })

  it('returns null in the silence between cues', () => {
    // Leaving the last line on screen through a pause reads as a stuck player.
    expect(currentCueText(cues, 5)).toBeNull()
  })

  it('returns null before the first cue', () => {
    expect(currentCueText(cues, -1)).toBeNull()
  })

  it('returns null when there are no cues', () => {
    expect(currentCueText([], 5)).toBeNull()
  })
})
