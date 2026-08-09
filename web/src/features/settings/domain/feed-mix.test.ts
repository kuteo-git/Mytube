import { describe, expect, it } from 'vitest'
import {
  adjustablePercent,
  FALLBACK_FIXED_SHARES,
  type FeedMix,
  type FixedShares,
  mixTotal,
  setShare,
  videosPerWindow,
} from './feed-mix'

const balanced: FeedMix = {
  subscribedPercent: 25,
  affinityPercent: 60,
  discoveryPercent: 15,
}

describe('setShare', () => {
  it('keeps the three adding to a hundred', () => {
    for (const value of [0, 7, 33, 50, 99, 100]) {
      expect(mixTotal(setShare(balanced, 'subscribedPercent', value))).toBe(100)
      expect(mixTotal(setShare(balanced, 'discoveryPercent', value))).toBe(100)
    }
  })

  it('sets the share that was dragged to exactly what was asked for', () => {
    expect(setShare(balanced, 'discoveryPercent', 40).discoveryPercent).toBe(40)
  })

  it('leaves the other two in the ratio they were already in', () => {
    // 60 against 25 before; still roughly 2.4 to 1 after, rather than the drag
    // silently deciding the viewer now prefers one over the other.
    const next = setShare(balanced, 'discoveryPercent', 50)
    const ratio = next.affinityPercent / next.subscribedPercent
    expect(ratio).toBeGreaterThan(2.2)
    expect(ratio).toBeLessThan(2.6)
  })

  it('splits evenly when there is no ratio to preserve', () => {
    const allSubscribed: FeedMix = {
      subscribedPercent: 100,
      affinityPercent: 0,
      discoveryPercent: 0,
    }
    const next = setShare(allSubscribed, 'subscribedPercent', 60)
    expect(next.affinityPercent).toBe(20)
    expect(next.discoveryPercent).toBe(20)
    expect(mixTotal(next)).toBe(100)
  })

  it('clamps rather than accepting nonsense', () => {
    expect(setShare(balanced, 'subscribedPercent', 140).subscribedPercent).toBe(100)
    expect(setShare(balanced, 'subscribedPercent', -20).subscribedPercent).toBe(0)
    expect(setShare(balanced, 'subscribedPercent', Number.NaN).subscribedPercent).toBe(0)
  })

  it('reaches a hundred on one share, which is a legitimate way to watch', () => {
    const only = setShare(balanced, 'subscribedPercent', 100)
    expect(only).toEqual({
      subscribedPercent: 100,
      affinityPercent: 0,
      discoveryPercent: 0,
    })
  })
})

describe('videosPerWindow', () => {
  const fixed = FALLBACK_FIXED_SHARES

  // The readout beside each slider. A percentage of a percentage of a page is
  // arithmetic nobody should do in their head to learn what the setting does.
  it('counts against the adjustable share, not the whole page', () => {
    expect(videosPerWindow(100, fixed)).toBe(Math.round((adjustablePercent(fixed) / 100) * 24))
    expect(videosPerWindow(0, fixed)).toBe(0)
  })

  it('gives the default mix a recognisable first page', () => {
    // 60/20/20 over the 72% adjustable share of 24 slots.
    expect(videosPerWindow(60, fixed)).toBe(10)
    expect(videosPerWindow(20, fixed)).toBe(3)
  })

  // The regression this file exists to prevent from happening twice. The page
  // carried its own copy of the fixed shares and went on quoting 82% after the
  // fresh-subscribed share took ten of it, so every readout was a seventh too
  // high — on the one screen whose whole job is to say what the numbers mean.
  it('follows the fixed shares rather than a number written here', () => {
    const wider: FixedShares = { ...fixed, freshSubscribed: 30 }
    expect(adjustablePercent(wider)).toBe(52)
    expect(videosPerWindow(100, wider)).toBeLessThan(videosPerWindow(100, fixed))
  })

  // Somebody can set the fresh share high enough to leave nothing. The readout
  // has to say zero rather than go negative.
  it('cannot report a negative page', () => {
    const swallowed: FixedShares = { continueWatching: 50, rewatch: 40, freshSubscribed: 40 }
    expect(adjustablePercent(swallowed)).toBe(0)
    expect(videosPerWindow(100, swallowed)).toBe(0)
  })
})
