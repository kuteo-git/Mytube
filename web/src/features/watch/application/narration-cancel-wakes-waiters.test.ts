import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  cancelTranslationPass,
  loadViSubtitles,
  resetNarration,
  whenCuesArrive,
  whenCuesReady,
} from './narration'

/**
 * Cancelling the translation pass must not tell everybody there are no cues.
 *
 * The reported fault: the panel said "Speech not started" on a video that was
 * audibly being read aloud. The status was telling the truth — the
 * pre-generation sweep really had not begun — and the log said why:
 * `pregen not started cues=0`, on a video whose cues were loaded.
 *
 * `cancelTranslationPass` released its waiters with a literal `[]`. For the
 * pass that is harmless: it bumps the generation first, so the pass exits at
 * its generation check without ever reading the list. The sweep has no
 * generation to check. It read the length, concluded the video had no
 * subtitles, and returned — leaving its phase at `idle` for the rest of the
 * video, with narration supplied by the on-demand path underneath it and
 * nothing anywhere to say the sweep had quietly stood down.
 */

const VTT = ['WEBVTT', '', '00:00:01.000 --> 00:00:03.000', 'xin chào.', ''].join('\n')

beforeEach(() => {
  resetNarration()
  vi.stubGlobal('fetch', async () => ({ ok: true, text: async () => VTT }) as Response)
})

afterEach(() => {
  vi.unstubAllGlobals()
  resetNarration()
})

describe('cancelTranslationPass', () => {
  it('wakes waiters with the cues that are loaded, not with an empty list', async () => {
    // The sweep's position exactly: waiting on a load already in flight.
    // Asked for *after* the fetch starts, because a request nobody has made is
    // answered immediately with an empty list — a different rule, for a
    // different fault, recorded in narration-cues-nobody-fetches.test.ts.
    loadViSubtitles('/media/v/1080p.mp4.vi.vtt', 'vi')
    const waiting = whenCuesReady()
    await vi.waitFor(async () => expect((await whenCuesReady()).length).toBe(1))

    cancelTranslationPass()

    expect((await waiting).length).toBe(1)
  })

  it('still says empty where there really are no cues', async () => {
    vi.stubGlobal('fetch', async () => ({ ok: false, text: async () => '' }) as Response)
    loadViSubtitles('/media/v/1080p.mp4.vi.vtt', 'vi')
    const waiting = whenCuesReady()
    cancelTranslationPass()

    expect(await waiting).toEqual([])
  })
})

/**
 * The sweep asks before anything is fetching, and must not be told "no".
 *
 * The effect that starts pre-generation is declared above the effect that loads
 * the cues, so this ordering is the ordinary one rather than a race that
 * sometimes happens. `whenCuesReady` answers `[]` immediately when nothing is
 * in flight — right for the pass, fatal for the sweep, which has one chance.
 */
describe('whenCuesArrive', () => {
  it('waits through an empty answer for cues that have not been asked for yet', async () => {
    const arriving = whenCuesArrive()

    // Exactly what the pass does on its way past: wakes every waiter, with
    // nothing, because nobody had started a fetch.
    cancelTranslationPass()
    loadViSubtitles('/media/v/1080p.mp4.vi.vtt', 'vi')

    expect((await arriving).length).toBe(1)
  })
})
