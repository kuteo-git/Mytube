import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { loadViSubtitles, resetNarration, whenCuesReady } from './narration'

/**
 * Asking for the same subtitles twice must not throw away the first request.
 *
 * The reported fault: opening a video with narration on left the status at
 * "Loading subtitles…" against subtitles plainly listed on screen, and picking
 * a different track was what made it start.
 *
 * The mechanism is a race with itself. `loadViSubtitles` skipped work only when
 * the cues were already loaded — which is exactly false while a fetch is in
 * flight — so a second call for the *same* URL bumped the generation, and the
 * response already on its way was dropped at the generation check. The cues
 * stayed null, so the next call did the same thing.
 *
 * Nothing about that is one-off. The subtitle list arrives from a query and the
 * player polls while a video downloads, so the effect that calls this ran again
 * on every refetch with identical data. Only a change of URL broke the cycle,
 * which is why choosing another track looked like the fix.
 */

const VTT = [
  'WEBVTT',
  '',
  '00:00:01.000 --> 00:00:03.000',
  'hello there.',
  '',
].join('\n')

let inFlight: Array<(body: string) => void> = []
let requests = 0

beforeEach(() => {
  resetNarration()
  inFlight = []
  requests = 0
  // A fetch that does not resolve until the test says so, which is the only
  // way to be inside the window this bug lives in.
  vi.stubGlobal('fetch', (url: string) => {
    requests++
    void url
    return new Promise((resolve) => {
      inFlight.push((body) => resolve({ ok: true, text: async () => body } as Response))
    })
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
  resetNarration()
})

describe('loading subtitles twice', () => {
  it('does not restart a request already in flight for the same URL', async () => {
    loadViSubtitles('/subs/en.vtt', 'en')
    loadViSubtitles('/subs/en.vtt', 'en')
    loadViSubtitles('/subs/en.vtt', 'en')

    expect(requests).toBe(1)
  })

  /**
   * The consequence, which is what anybody actually saw: a waiter that never
   * resolves. This is the assertion that would have failed before the fix.
   */
  it('still delivers the cues after a repeated call', async () => {
    loadViSubtitles('/subs/en.vtt', 'en')
    const waiting = whenCuesReady()

    // The repeat that used to invalidate the response below.
    loadViSubtitles('/subs/en.vtt', 'en')

    inFlight[0]?.(VTT)
    const cues = await waiting

    expect(cues).toHaveLength(1)
    expect(cues[0].text).toContain('hello there')
  })

  it('does start over for a different URL, which is a different video', async () => {
    loadViSubtitles('/subs/en.vtt', 'en')
    loadViSubtitles('/subs/other.vtt', 'en')

    expect(requests).toBe(2)
  })
})
