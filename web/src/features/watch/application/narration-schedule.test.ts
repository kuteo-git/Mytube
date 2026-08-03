import { describe, expect, it } from 'vitest'
import {
  DEFAULT_SPEED,
  MAX_SPEED,
  MIN_SLOT_SECONDS,
  shouldPlay,
  slotFor,
  speedFor,
  startTimeFor,
  latenessOf,
  scheduleAt,
  tooLateToPlay,
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

describe('lateness introduced by the clip before', () => {
  it('accepts a clip that lands on its own moment', () => {
    expect(latenessOf(10, 10)).toBe(0)
    expect(tooLateToPlay(10, 10)).toBe(false)
  })

  it('tolerates a small push', () => {
    // Clips run over their slot constantly and a fraction of a second late is
    // not worth silencing a line for.
    expect(tooLateToPlay(10.4, 10)).toBe(false)
  })

  it('drops a clip pushed past the tolerance', () => {
    // This is the drift: a cue whose audio does not fit even at 3x pushes
    // _scheduledUntil past the next cue, which pushes the one after that.
    // Nothing in the old code ever compared the scheduled moment against the
    // cue it belonged to, so the gap only ever grew.
    expect(tooLateToPlay(11.5, 10)).toBe(true)
  })

  it('never treats an early clip as late', () => {
    expect(tooLateToPlay(9, 10)).toBe(false)
    expect(latenessOf(9, 10)).toBe(0)
  })
})

describe('scheduleAt', () => {
  it('leaves a breath when the clip before finished in good time', () => {
    // Previous ends at 9, cue due at 10: room for the gap and then some.
    expect(scheduleAt(10, 9)).toBe(10)
  })

  it('takes a shorter breath rather than start the line late', () => {
    // Only 0.1s of room before this cue is due. Insisting on the full gap
    // would delay the line to 10.15 for the sake of 0.15s of silence — and
    // that delay is exactly what the next clip then inherits.
    expect(scheduleAt(10, 9.9)).toBeCloseTo(10, 5)
  })

  it('gives up the breath rather than pass an overrun on', () => {
    // Previous ran past this cue's moment. Adding a gap on top is what turned
    // one overrun into drift that compounded for the rest of the video.
    expect(scheduleAt(10, 10.3)).toBeCloseTo(10.3, 5)
  })

  it('never starts a clip before it is due', () => {
    expect(scheduleAt(10, 0)).toBe(10)
  })
})
