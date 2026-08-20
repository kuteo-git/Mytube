import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import {
  DEFAULT_LIVE_HEIGHT,
  PINNED_HEIGHT,
  availableTiers,
  openingTier,
  targetTier,
  tierLabel,
} from './player-source'

/**
 * The source decisions, asked directly.
 *
 * Until now the only way to ask any of this was to render a watch page against
 * a mocked repository and read the answer off a `<video>` element — about a
 * second per question, and every question mixed up with autoplay, narration and
 * the audio graph. The five files of tier tests in this directory are all that
 * shape. These take a millisecond and say what they mean.
 */

/** A desktop: MediaSource, no native HLS. jsdom claims to be a Safari otherwise. */
function asDesktop() {
  Object.defineProperty(window.navigator, 'vendor', { configurable: true, get: () => 'Google Inc.' })
  Object.defineProperty(window.navigator, 'userAgent', {
    configurable: true,
    get: () => 'Mozilla/5.0 Chrome/140.0.0.0',
  })
  ;(window as unknown as Record<string, unknown>).MediaSource = function () {}
}

/** A browser with neither path — no native HLS and no MediaSource. */
function asAncientBrowser() {
  Object.defineProperty(window.navigator, 'vendor', { configurable: true, get: () => 'Google Inc.' })
  Object.defineProperty(window.navigator, 'userAgent', {
    configurable: true,
    get: () => 'Mozilla/5.0 Chrome/40.0.0.0',
  })
  delete (window as unknown as Record<string, unknown>).MediaSource
  delete (window as unknown as Record<string, unknown>).ManagedMediaSource
}

const hls = { url: '/api/videos/a/hls/master.m3u8', height: 720, seekable: true }
const local = { url: '/media/a/1080p.mp4', seekable: true }

beforeEach(asDesktop)
afterEach(() => {
  delete (window as unknown as Record<string, unknown>).MediaSource
})

describe('availableTiers', () => {
  it('offers the file on disk first', () => {
    const tiers = availableTiers({ local, hls }, 'auto')
    expect(tiers.map((t) => t.name)).toEqual(['local', 'hls'])
  })

  it('offers HLS alone while the download is still running', () => {
    const tiers = availableTiers({ hls }, 'auto')
    expect(tiers.map((t) => t.name)).toEqual(['hls'])
  })

  it('is empty when the answer carries nothing', () => {
    expect(availableTiers(undefined, 'auto')).toEqual([])
    expect(availableTiers({}, 'auto')).toEqual([])
  })

  /**
   * The muxed stream is no longer a tier at all, so a server still offering one
   * changes nothing here. That is what lets the offsets, marks and handover
   * timing go — a tier that cannot seek is the only reason any of it existed.
   */
  it('ignores a muxed stream even when the server still offers one', () => {
    const tiers = availableTiers(
      { hls, remux: { url: '/api/videos/a/remux', height: 720, seekable: false } },
      'auto',
    )
    expect(tiers.map((t) => t.name)).toEqual(['hls'])
  })

  /**
   * A browser that can play neither gets no upstream tier and waits for the
   * download — the same answer §4 already gives when upstream has nothing
   * playable. Offering an unseekable stream to reach it would bring back every
   * mechanism this change removes.
   */
  it('offers nothing upstream to a browser that can play neither', () => {
    asAncientBrowser()
    expect(availableTiers({ hls }, 'auto')).toEqual([])
    // The file on disk is still perfectly playable there.
    expect(availableTiers({ local, hls }, 'auto').map((t) => t.name)).toEqual(['local'])
  })

  it('asks for the high rendition only when it was pinned', () => {
    expect(availableTiers({ hls }, 'auto')[0].height).toBe(DEFAULT_LIVE_HEIGHT)
    expect(availableTiers({ hls }, 'high')[0].height).toBe(PINNED_HEIGHT)
  })
})

describe('openingTier', () => {
  it('opens on the disk when the file is there', () => {
    expect(openingTier(availableTiers({ local, hls }, 'auto'))?.name).toBe('local')
  })

  it('opens on HLS when it is not', () => {
    expect(openingTier(availableTiers({ hls }, 'auto'))?.name).toBe('hls')
  })

  it('opens on nothing when there is nothing', () => {
    expect(openingTier([])).toBeUndefined()
  })
})

describe('targetTier', () => {
  it('moves to the local file when the download lands mid-watch', () => {
    const tiers = availableTiers({ local, hls }, 'auto')
    expect(targetTier(tiers, 'hls', false)?.name).toBe('local')
  })

  it('stays put once it is on the best source', () => {
    const tiers = availableTiers({ local, hls }, 'auto')
    expect(targetTier(tiers, 'local', false)).toBeUndefined()
  })

  it('wants nothing while only the stream exists', () => {
    const tiers = availableTiers({ hls }, 'auto')
    expect(targetTier(tiers, 'hls', false)).toBeUndefined()
  })

  /**
   * A drive that has gone away fails the same way for ever, so a file that will
   * not load is not asked for again — the viewer keeps the stream that is
   * working rather than being moved onto a dead source once per poll.
   */
  it('does not keep reaching for a local file that will not load', () => {
    const tiers = availableTiers({ local, hls }, 'auto')
    expect(targetTier(tiers, 'hls', true)).toBeUndefined()
  })
})

describe('tierLabel', () => {
  it('says the height, and says when the picture is coming off the network', () => {
    expect(tierLabel({ name: 'hls', url: '', height: 720 })).toBe('720p live')
    expect(tierLabel({ name: 'hls', url: '', height: 1080 })).toBe('1080p live')
    expect(tierLabel({ name: 'local', url: '' }, 1080)).toBe('1080p')
    expect(tierLabel(undefined)).toBe('')
  })
})
