import { describe, expect, it } from 'vitest'
import {
  DEFAULT_SPEED,
  MAX_SPEED,
  MIN_SLOT_SECONDS,
  shouldPlay,
  slotFor,
  speedFor,
  startTimeFor,
} from './narration-schedule'
import type { CueText } from './narration-vtt'

const cue = (start: number, end: number, text = 'x'): CueText => ({ start, end, text })

describe('slotFor', () => {
  it('gives a line the silence after it, not just its own timing', () => {
    // The reported case. Subtitles are timed to the words on screen; the pause
    // that follows belongs to nobody, and reading against the one second
    // between the first line's own timestamps hurries a sentence that had
    // three seconds of room.
    const cues = [cue(1, 2, 'xin chào các bạn tôi là AI'), cue(4, 6, 'các bạn khoẻ không?')]
    expect(slotFor(cues, 0)).toBe(3)
  })

  it('does not cap the slot at a multiple of the cue', () => {
    // The old rule was min(gap, ownLength × 2), which returned 2 for the case
    // above — still hurried, and the 2 stood for nothing.
    const cues = [cue(1, 2), cue(10, 12)]
    expect(slotFor(cues, 0)).toBe(9)
  })

  it('stops at the next line, never over it', () => {
    const cues = [cue(0, 5), cue(3, 6)]
    // Overlapping cues should not hand the first one time the second is using.
    expect(slotFor(cues, 0)).toBe(3)
  })

  it('falls back to the cue itself for the last line', () => {
    const cues = [cue(1, 2), cue(4, 6.5)]
    expect(slotFor(cues, 1)).toBe(2.5)
  })

  it('never returns zero or less', () => {
    const cues = [cue(5, 5), cue(5, 6)]
    expect(slotFor(cues, 0)).toBe(MIN_SLOT_SECONDS)
  })

  it('answers for an index that does not exist', () => {
    expect(slotFor([], 0)).toBe(MIN_SLOT_SECONDS)
  })
})

describe('shouldPlay', () => {
  it('plays a clip that arrived in time', () => {
    expect(shouldPlay(10, 9.5)).toBe(true)
  })

  it('drops a clip whose moment has gone', () => {
    // Playing it late is what stacks it on the line after it, and pushes
    // everything behind further out still.
    expect(shouldPlay(10, 10.5)).toBe(false)
  })

  it('allows a clip that is exactly on time', () => {
    expect(shouldPlay(10, 10)).toBe(true)
  })
})

describe('speedFor', () => {
  it('reads at the default when there is room', () => {
    expect(speedFor(2, 10)).toBe(DEFAULT_SPEED)
  })

  it('speeds up only as much as the slot demands', () => {
    expect(speedFor(4, 2)).toBeCloseTo(2)
  })

  it('never exceeds the maximum, however tight the slot', () => {
    expect(speedFor(60, 1)).toBe(MAX_SPEED)
  })

  it('never drawls below the default', () => {
    expect(speedFor(0.5, 10)).toBe(DEFAULT_SPEED)
  })

  it('survives nonsense input', () => {
    expect(speedFor(0, 5)).toBe(DEFAULT_SPEED)
    expect(speedFor(5, 0)).toBe(DEFAULT_SPEED)
    expect(speedFor(Number.NaN, 5)).toBe(DEFAULT_SPEED)
  })
})

describe('startTimeFor', () => {
  it('converts a cue time into a moment on the audio clock', () => {
    // Video is at 10s, cue is due at 12s, audio clock reads 100 → 102.
    expect(startTimeFor(12, 10, 100)).toBe(102)
  })

  it('never schedules into the past', () => {
    expect(startTimeFor(8, 10, 100)).toBe(100)
  })
})
