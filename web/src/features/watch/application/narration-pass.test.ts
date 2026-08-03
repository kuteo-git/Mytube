import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  cancelTranslationPass,
  loadViSubtitles,
  narrationCues,
  narrationProgress,
  nearestCueIndex,
  resetNarration,
  startTranslationPass,
  stopNarrationPlayback,
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

describe('cues survive the output mode changing', () => {
  afterEach(() => {
    resetNarration()
    vi.unstubAllGlobals()
  })

  const VTT = `WEBVTT

00:00:01.000 --> 00:00:04.000
Hello there everyone.

00:00:05.000 --> 00:00:09.000
This is the second line.
`

  async function loadCues() {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({ ok: true, text: async () => VTT }),
    )
    loadViSubtitles('/media/abc/en.vtt', 'en')
    await new Promise((r) => setTimeout(r, 10))
  }

  it('keeps them when the voice is switched off', async () => {
    // "Giọng đọc" to "Phụ đề" tears down the tick loop. That used to discard
    // the cue list, and the effect that loads cues had no reason to run again —
    // so switching back produced narration that never requested a single clip.
    await loadCues()
    expect(narrationCues().length).toBe(2)

    stopNarrationPlayback()

    expect(narrationCues().length).toBe(2)
  })

  it('forgets them only when the video is left behind', async () => {
    await loadCues()
    resetNarration()
    expect(narrationCues().length).toBe(0)
  })

  it('can be stopped and resumed repeatedly without losing them', async () => {
    // The report was that this happened when flipping options back and forth.
    await loadCues()
    for (let i = 0; i < 5; i++) stopNarrationPlayback()
    expect(narrationCues().length).toBe(2)
  })
})
