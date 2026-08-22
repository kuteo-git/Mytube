import { describe, expect, it } from 'vitest'

import { atLiveEdge, livePercent } from './live-timeline'

describe('a broadcast timeline', () => {
  /**
   * The bug, exactly as it was seen: a broadcast 25:57 in, no duration, a bar
   * painted solid red end to end.
   */
  it('does not paint the bar full on a stream with no declared duration', () => {
    expect(livePercent(null, 1557)).toBe(0)
  })

  it('measures from the window, not from zero', () => {
    // Halfway through a window running 600..1200.
    expect(livePercent({ start: 600, end: 1200 }, 900)).toBe(50)
  })

  /**
   * The window slides forward, so a position can fall out of the bottom of it
   * between two reads. That is a bar at the start, not a negative one.
   */
  it('clamps a position the window has already left behind', () => {
    expect(livePercent({ start: 600, end: 1200 }, 400)).toBe(0)
  })

  it('clamps a position past the edge rather than overflowing', () => {
    expect(livePercent({ start: 0, end: 1285 }, 1290)).toBe(100)
  })

  describe('being at the edge', () => {
    it('is true a couple of segments behind, because the edge keeps moving', () => {
      // Measured: playback settled at t=1270.1 against a window ending 1285.
      expect(atLiveEdge({ start: 0, end: 1285 }, 1278)).toBe(true)
    })

    it('is false once the viewer has rewound', () => {
      expect(atLiveEdge({ start: 0, end: 1285 }, 1216)).toBe(false)
    })

    it('is false before the window is known, so nothing claims to be live', () => {
      expect(atLiveEdge(null, 1557)).toBe(false)
    })
  })
})
