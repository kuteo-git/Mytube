import { describe, expect, it } from 'vitest'
import {
  DISMISS_FADE_FRACTION,
  dismissFade,
  layerOpacity,
} from './watch-overlay'

describe('clearing the watch layer away', () => {
  it('is complete within a small share of the screen', () => {
    // Deliberately small: everything except the player goes almost at once, so
    // the rest of the drag is a picture travelling across a page that is
    // already there rather than a page slowly dissolving.
    expect(dismissFade(800 * DISMISS_FADE_FRACTION, 800)).toBe(1)
  })

  it('is measured against the screen, not the player or the journey', () => {
    // The number is about how far a hand has moved, and a hand does not know
    // how tall the player is.
    expect(dismissFade(40, 800)).toBeCloseTo(0.5)
    expect(dismissFade(40, 1600)).toBeCloseTo(0.25)
  })

  it('is nothing before the drag starts', () => {
    expect(dismissFade(0, 800)).toBe(0)
    expect(dismissFade(-50, 800)).toBe(0)
  })

  it('does not keep going past gone', () => {
    // The player travels for the rest of the drag; this does not.
    expect(dismissFade(9999, 800)).toBe(1)
  })

  it('survives a viewport of zero rather than dividing by it', () => {
    // jsdom, and the first frame of a page that has not been laid out.
    expect(dismissFade(50, 0)).toBe(0)
  })
})

describe('what the fade drives', () => {
  it('takes the layer out exactly as it brings the bar in', () => {
    // One number seen from two sides. As two, the navigation would arrive at a
    // different moment from the page it belongs to.
    for (const fade of [0, 0.25, 0.5, 1]) {
      expect(layerOpacity(fade) + fade).toBeCloseTo(1)
    }
  })

  it('is fully present before anything has happened', () => {
    expect(layerOpacity(0)).toBe(1)
  })

  it('is fully gone at the end', () => {
    expect(layerOpacity(1)).toBe(0)
  })
})
