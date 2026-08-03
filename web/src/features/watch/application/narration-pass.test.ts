import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  cancelTranslationPass,
  narrationProgress,
  nearestCueIndex,
  resetNarration,
  startTranslationPass,
} from './narration'
import type { CueText } from './narration-vtt'

describe('who is allowed to end a translation pass', () => {
  afterEach(() => {
    cancelTranslationPass()
    vi.unstubAllGlobals()
  })

  it('survives resetNarration, which fires on every layer swap', () => {
    // resetNarration runs from the cleanup of the narration tick effect, and
    // that effect tears down whenever the front <video> changes identity — a
    // layer swap, not a new video. Cancelling the pass there killed it seconds
    // after it started, leaving the status on "not started" with nothing to
    // restart it. This is the second time that shape of bug has shipped.
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
    startTranslationPass('vid1', 0)
    expect(narrationProgress().phase).toBe('waiting-subtitles')

    resetNarration()

    expect(narrationProgress().phase).toBe('waiting-subtitles')
    expect(narrationProgress().running).toBe(true)
  })

  it('ends when the pass is cancelled outright', () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
    startTranslationPass('vid1', 0)

    cancelTranslationPass()

    expect(narrationProgress().phase).toBe('idle')
    expect(narrationProgress().running).toBe(false)
  })

  it('can be started again after a cancel', () => {
    // The running flag being stuck is the one state from which no pass ever
    // starts again, so a cancel has to leave it clear.
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => new Promise(() => {})))
    startTranslationPass('vid1', 0)
    cancelTranslationPass()

    startTranslationPass('vid1', 0)

    expect(narrationProgress().running).toBe(true)
    expect(narrationProgress().phase).toBe('waiting-subtitles')
  })
})

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
