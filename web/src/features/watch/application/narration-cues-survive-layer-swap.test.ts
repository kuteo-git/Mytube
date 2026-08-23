import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  loadViSubtitles,
  resetNarration,
  stopNarrationPlayback,
  whenCuesReady,
} from './narration'

/**
 * Stopping the voice must not throw away the subtitles being fetched.
 *
 * `stopNarrationPlayback` says in its own name that it keeps the cues, and it
 * fires far more often than the name suggests: it is the narration tick's
 * teardown, and that tears down on every swap between the two <video> layers —
 * which is what a video does while it is still downloading.
 *
 * It shared a generation counter with the cue fetch. So a swap landing while
 * the VTT was in flight dropped the response at its generation check, left
 * `_cuesLoading` true with nothing running, and left `_cuesURL` holding the
 * address — so the reload was a no-op for ever after. `whenCuesReady` then
 * handed the pass a promise no code path could resolve, and the status sat on
 * "Loading subtitles…" for the life of the page.
 *
 * Reported three times, always on a video opened from the up-next rail: those
 * are the ones still downloading, and the ones whose subtitles arrive after the
 * page does.
 */

const VTT = ['WEBVTT', '', '00:00:01.000 --> 00:00:03.000', 'hello.', ''].join('\n')

let release: () => void

beforeEach(() => {
  resetNarration()
  // A fetch that has not answered yet, which is the whole of the fault: the
  // window between asking for the cues and getting them is where a layer swap
  // lands.
  vi.stubGlobal(
    'fetch',
    () =>
      new Promise<Response>((resolve) => {
        release = () => resolve({ ok: true, text: async () => VTT } as Response)
      }),
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
  resetNarration()
})

function within(ms: number, p: Promise<unknown>) {
  return Promise.race([
    p,
    new Promise((_, reject) =>
      setTimeout(() => reject(new Error('whenCuesReady never settled')), ms),
    ),
  ])
}

describe('a layer swap while the subtitles are still on the way', () => {
  it('still delivers them to a pass already waiting', async () => {
    loadViSubtitles('/subs/en.vtt', 'en')
    const waiting = whenCuesReady()

    // The download lands and the player swaps which element is in front.
    stopNarrationPlayback()

    release()
    const cues = (await within(200, waiting)) as { text: string }[]
    expect(cues.map((c) => c.text)).toEqual(['hello.'])
  })

  it('and to a pass that starts after the swap', async () => {
    loadViSubtitles('/subs/en.vtt', 'en')
    stopNarrationPlayback()
    release()

    const cues = (await within(200, whenCuesReady())) as { text: string }[]
    expect(cues.map((c) => c.text)).toEqual(['hello.'])
  })

  it('and a reset still ends the wait rather than leaving it hanging', async () => {
    // The other half of the same counter: leaving the video behind must not let
    // an answer for it arrive as the cues of the video just opened.
    loadViSubtitles('/subs/en.vtt', 'en')
    resetNarration()
    release()
    await expect(within(200, whenCuesReady())).resolves.toEqual([])
  })
})
