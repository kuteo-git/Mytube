import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { loadViSubtitles, resetNarration, whenCuesReady } from './narration'

/**
 * The pass must never wait on a load that nobody started.
 *
 * The reported fault, twice over: the status line sat on "Loading subtitles…"
 * for a video whose subtitle file was on disk, served a 200, and was listed in
 * the caption menu.
 *
 * The mechanism is two pieces of state that are reset by different things.
 * `resetNarration()` clears the cues, and it fires when the video changes.
 * `loadViSubtitles()` refetches them, and the effect that calls it fires when
 * the subtitle *addresses* change. Whenever those two do not coincide — the
 * cues cleared, the addresses the same — nothing is left to fetch anything, and
 * `whenCuesReady()` handed out a promise that could not be resolved by any code
 * path. The pass held there for the life of the page.
 *
 * The waiting had no way to end because nothing recorded whether a fetch was
 * actually in flight. It does now.
 */

const VTT = ['WEBVTT', '', '00:00:01.000 --> 00:00:03.000', 'hello.', ''].join('\n')

beforeEach(() => {
  resetNarration()
  vi.stubGlobal('fetch', async () => ({ ok: true, text: async () => VTT }) as Response)
})

afterEach(() => {
  vi.unstubAllGlobals()
  resetNarration()
})

/** Fails rather than hanging the suite, which is what the bug does to a page. */
function within(ms: number, p: Promise<unknown>) {
  return Promise.race([
    p,
    new Promise((_, reject) =>
      setTimeout(() => reject(new Error('whenCuesReady never settled')), ms),
    ),
  ])
}

describe('when no fetch is in flight', () => {
  it('answers rather than waiting for one that will never start', async () => {
    // Exactly the state the reset leaves behind: no cues, no load running, and
    // no reason for the effect that loads them to run again.
    await expect(within(200, whenCuesReady())).resolves.toEqual([])
  })

  it('still waits properly for a fetch that is actually running', async () => {
    loadViSubtitles('/subs/en.vtt', 'en')
    const cues = (await within(200, whenCuesReady())) as { text: string }[]
    expect(cues.map((c) => c.text)).toEqual(['hello.'])
  })

  it('answers again after a reset, instead of holding the next pass', async () => {
    loadViSubtitles('/subs/en.vtt', 'en')
    await whenCuesReady()
    // The video changes. The addresses do not, so nothing reloads.
    resetNarration()
    await expect(within(200, whenCuesReady())).resolves.toEqual([])
  })
})
