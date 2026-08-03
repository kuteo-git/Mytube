import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  hashCue,
  loadNarrationCache,
  saveNarrationCache,
  setCachePartition,
} from './narration-cache'

// Translations are filed under the model that produced them, so a partition has
// to be chosen before any of these calls mean anything.
beforeEach(() => setCachePartition('m1'))

afterEach(() => vi.unstubAllGlobals())

describe('hashCue', () => {
  it('is stable and differs on different text', async () => {
    const a = await hashCue('Hello there.')
    const b = await hashCue('Hello there.')
    const c = await hashCue('Hello there!')
    expect(a).toBe(b)
    expect(a).not.toBe(c)
    expect(a).toMatch(/^[0-9a-f]{40}$/)
  })

  it('works where crypto.subtle does not exist', async () => {
    // Plain HTTP on a LAN address is not a secure context, so crypto.subtle is
    // undefined there. Depending on it took the whole translation pass down on
    // the first cue, while localhost — exempt from the rule — passed every test.
    vi.stubGlobal('crypto', {})
    expect(await hashCue('abc')).toBe('a9993e364706816aba3e25717850c26c9cd0d89d')
  })
})

describe('loadNarrationCache', () => {
  it('returns the entries as a map', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ entries: { h1: 'xin chào' } }),
      }),
    )
    const got = await loadNarrationCache('vid')
    expect(got.get('h1')).toBe('xin chào')
  })

  it('returns an empty map when the request fails', async () => {
    // MEDIA_ROOT can be an unmounted SSD. Narration must still work.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')))
    const got = await loadNarrationCache('vid')
    expect(got.size).toBe(0)
  })
})

describe('saveNarrationCache', () => {
  it('posts the engine and entries', async () => {
    const f = vi.fn().mockResolvedValue({ ok: true, json: async () => ({}) })
    vi.stubGlobal('fetch', f)
    await saveNarrationCache('vid', new Map([['h1', 'chào']]))
    const [url, init] = f.mock.calls[0]
    expect(url).toBe('/api/videos/vid/narration-cache')
    expect(JSON.parse(init.body)).toEqual({
      engine: 'omniroute:m1',
      entries: { h1: 'chào' },
    })
  })

  it('does not throw when the write fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('no disk')))
    await expect(
      saveNarrationCache('vid', new Map([['h', 'x']])),
    ).resolves.toBeUndefined()
  })

  it('skips the request entirely when there is nothing to write', async () => {
    const f = vi.fn()
    vi.stubGlobal('fetch', f)
    await saveNarrationCache('vid', new Map())
    expect(f).not.toHaveBeenCalled()
  })
})
