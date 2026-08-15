import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  cancelTranslationPass,
  narrationProgress,
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
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({ entries: {} }) })),
    )
    setCachePartition('a-model')
    startTranslationPass('vid3', 0)
    await settle()

    expect(narrationProgress().phase).toBe('waiting-subtitles')
  })
})
