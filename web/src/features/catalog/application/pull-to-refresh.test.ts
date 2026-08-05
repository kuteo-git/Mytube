import { describe, expect, it } from 'vitest'
import {
  MAX_PULL,
  REFRESH_THRESHOLD,
  canPull,
  pullDistance,
  pullProgress,
  shouldRefresh,
} from './pull-to-refresh'

describe('when a downward drag is a pull', () => {
  it('is only from the very top', () => {
    expect(canPull(0)).toBe(true)
  })

  it('is not one pixel down the page', () => {
    // The same movement there means "scroll back up", which is a far more
    // common thing to want. Taking it would make the feed feel stuck.
    expect(canPull(1)).toBe(false)
    expect(canPull(900)).toBe(false)
  })

  it('is one at a negative offset, which a rubber band produces', () => {
    expect(canPull(-20)).toBe(true)
  })
})

describe('how far the page follows the finger', () => {
  it('does not move at all upward', () => {
    expect(pullDistance(-40)).toBe(0)
    expect(pullDistance(0)).toBe(0)
  })

  it('arms at about the travel a thumb expects', () => {
    // The only measurement that matters to a hand, and the one the curve is
    // tuned to hold: how far it has to move before letting go does something.
    // An earlier version asserted "well under half the movement" instead, which
    // is a proxy for resistance and quietly fought this — resistance stiff
    // enough to satisfy it needed 230px of travel to arm, which is most of a
    // phone screen.
    expect(pullDistance(60)).toBeLessThan(REFRESH_THRESHOLD)
    expect(pullDistance(120)).toBeGreaterThanOrEqual(REFRESH_THRESHOLD)
  })

  it('answers immediately, before it starts resisting', () => {
    // Resistance from the first pixel reads as the gesture not having been
    // noticed at all.
    expect(pullDistance(4)).toBeGreaterThan(3.5)
  })

  it('gets heavier the harder it is pulled', () => {
    // Resistance that builds is what tells a hand it has reached the end of
    // something. A constant fraction feels uniform and says nothing.
    const first = pullDistance(50) - pullDistance(0)
    const later = pullDistance(250) - pullDistance(200)
    expect(later).toBeLessThan(first)
  })

  it('stops at the cap however hard it is pulled', () => {
    // Past the threshold the answer is already yes, and a spinner still
    // sliding down the screen invites the viewer to keep pulling in case
    // something else happens.
    expect(pullDistance(10_000)).toBe(MAX_PULL)
    expect(pullDistance(400)).toBeLessThanOrEqual(MAX_PULL)
  })

  it('can be pulled far enough to arm at all', () => {
    // A curve that never reaches the threshold would be a gesture that cannot
    // be completed — the worst possible version of this.
    expect(pullDistance(300)).toBeGreaterThanOrEqual(REFRESH_THRESHOLD)
  })

  it('never moves the page more than the finger', () => {
    for (const dy of [10, 50, 120, 300]) {
      expect(pullDistance(dy)).toBeLessThanOrEqual(dy)
    }
  })
})

describe('deciding on release', () => {
  it('refetches once the threshold is reached', () => {
    expect(shouldRefresh(REFRESH_THRESHOLD)).toBe(true)
  })

  it('does nothing for a shorter pull', () => {
    expect(shouldRefresh(REFRESH_THRESHOLD - 1)).toBe(false)
    expect(shouldRefresh(0)).toBe(false)
  })
})

describe('what the indicator reports', () => {
  it('is nothing before the pull starts', () => {
    expect(pullProgress(0)).toBe(0)
  })

  it('is complete at the threshold, not at the cap', () => {
    // The control has to say what releasing will do *before* it is released —
    // the difference between a gesture that can be aborted and one that can
    // only be regretted.
    expect(pullProgress(REFRESH_THRESHOLD)).toBe(1)
  })

  it('does not keep climbing past complete', () => {
    expect(pullProgress(MAX_PULL)).toBe(1)
  })

  it('agrees with the decision at every point', () => {
    // A full-looking indicator over a pull that will not fire, or the reverse,
    // is the one thing this must never do.
    for (const d of [0, 20, 50, REFRESH_THRESHOLD - 1, REFRESH_THRESHOLD, 200]) {
      expect(pullProgress(d) >= 1).toBe(shouldRefresh(d))
    }
  })
})
