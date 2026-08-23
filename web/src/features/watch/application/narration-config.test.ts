import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  cancelTranslationPass,
  loadViSubtitles,
  narrationProgress,
  resetNarration,
  startTranslationPass,
} from './narration'
import { setCachePartition } from '@/features/watch/infrastructure/narration-cache'

/**
 * What the pass is doing before it translates its first line.
 *
 * All four steps used to report themselves as "Loading subtitles…", and the
 * first of them — waiting to be told which model is configured — never
 * finished at all when the answer was "none". The two faults read identically
 * from the outside: a status line quoting a subtitle file that had loaded long
 * before, for ever.
 */

/** Lets the microtask queue drain, which is where the pass advances. */
const settle = () => new Promise((r) => setTimeout(r, 0))

describe('the steps before the first batch', () => {
  afterEach(() => {
    cancelTranslationPass()
    // The cue list too, not just the pass. It is module state, so a test that
    // left a fetch pending would otherwise decide the next test's answer — the
    // same shared-state trap this whole file is about, one level up.
    resetNarration()
    vi.unstubAllGlobals()
  })

  it('starts by waiting for the translator settings', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})))
    startTranslationPass('vid1', 0)

    expect(narrationProgress().phase).toBe('waiting-config')
  })

  it('stops and says so when no model is configured', async () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})))
    startTranslationPass('vid1', 0)

    // The settings answered: there is nothing to translate with.
    setCachePartition('')
    await settle()

    // Not a wait, and not a failure of this video — an answer, and one whose
    // remedy is a settings screen.
    expect(narrationProgress().phase).toBe('no-translator')
    expect(narrationProgress().running).toBe(false)
  })

  it('moves on through the cache and the cues once there is a model', async () => {
    // Left pending, so the pass is caught in the middle of reading the cache.
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})))
    // The settings are known before this pass starts, which is the ordinary
    // case: the answer is fetched once and the module keeps it.
    setCachePartition('a-model')
    startTranslationPass('vid2', 0)
    await settle()

    // Named for the step it is actually on. This was 'waiting-subtitles' too.
    expect(narrationProgress().phase).toBe('reading-cache')
    expect(narrationProgress().running).toBe(true)
  })

  it('waits for the cues only after the cache has been read', async () => {
    // The cue fetch is left pending and the cache answers, so the pass is
    // caught on the step this is about. It has to be a *real* pending fetch:
    // waiting with nothing in flight is no longer a state the pass can be in,
    // because that was the fault — a promise no code path could resolve.
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) =>
        url.endsWith('.vtt')
          ? new Promise(() => {})
          : { ok: true, json: async () => ({ entries: {} }) },
      ),
    )
    setCachePartition('a-model')
    loadViSubtitles('/subs/vid3.en.vtt', 'en')
    startTranslationPass('vid3', 0)
    await settle()

    expect(narrationProgress().phase).toBe('waiting-subtitles')
  })

  it('does not sit on the cue step when nothing is fetching them', async () => {
    // The regression. The video changed, which cleared the cue list, while the
    // addresses did not, so the effect that reloads them never ran — and the
    // pass waited on a fetch nobody had started, for the life of the page. The
    // honest answer is that there are no cues; a later load announces them for
    // the pass after this one.
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({ entries: {} }) })),
    )
    setCachePartition('a-model')
    startTranslationPass('vid4', 0)
    await settle()

    expect(narrationProgress().phase).toBe('no-subtitles')
  })
})
