import { describe, expect, it } from 'vitest'
import {
  COMMIT_FRACTION,
  COMMIT_VELOCITY,
  DRAG_SLOP,
  isVerticalDrag,
  lerpRect,
  shouldCommit,
  travelProgress,
  velocityOf,
} from './player-gesture'

describe('telling a drag from a tap', () => {
  it('ignores movement inside the slop', () => {
    // A tap is what shows and hides the controls on touch, and a finger never
    // lands perfectly still. Without this every tap on the picture would nudge
    // the player.
    expect(isVerticalDrag(0, DRAG_SLOP - 1)).toBe(false)
    expect(isVerticalDrag(2, 3)).toBe(false)
  })

  it('admits a downward drag past the slop', () => {
    expect(isVerticalDrag(0, DRAG_SLOP + 1)).toBe(true)
    expect(isVerticalDrag(5, 40)).toBe(true)
  })

  it('never treats an upward movement as this gesture', () => {
    // Up is not a request to put the player away, and reading it as one would
    // make the picture flinch while somebody scrolls the page under it.
    expect(isVerticalDrag(0, -40)).toBe(false)
    expect(isVerticalDrag(3, -40)).toBe(false)
  })

  it('gives an ambiguous swipe to the horizontal', () => {
    // Sideways over a player belongs to seeking and to the browser's own back
    // gesture. Stealing an ambiguous swipe from either is worse than missing an
    // ambiguous drag.
    expect(isVerticalDrag(40, 40)).toBe(false)
    expect(isVerticalDrag(60, 30)).toBe(false)
  })
})

describe('how far along its journey it is', () => {
  it('is measured against the distance the player has to travel', () => {
    // Not against the player's own height, which is what the first version did
    // and what made the picture flee the finger: a phone player is ~220px tall
    // while the corner is ~640px away, so it arrived in the corner while the
    // hand was still half way down the screen.
    expect(travelProgress(160, 640)).toBeCloseTo(0.25)
  })

  it('puts the top edge exactly where the finger is', () => {
    // The property the whole gesture rests on. With the travel as denominator,
    // lerping the top by that fraction lands on start + dy — no constant tuned
    // until it looked close, just the arithmetic agreeing with the hand.
    const from = { top: 56, left: 0, width: 390, height: 220 }
    const to = { top: 696, left: 16, width: 358, height: 72 }
    const dy = 160
    const moved = lerpRect(from, to, travelProgress(dy, to.top - from.top))
    expect(moved.top).toBeCloseTo(from.top + dy)
  })

  it('is clamped at both ends', () => {
    expect(travelProgress(-40, 640)).toBe(0)
    expect(travelProgress(9999, 640)).toBe(1)
  })

  it('survives a travel of zero rather than dividing by it', () => {
    expect(travelProgress(50, 0)).toBe(0)
  })
})

describe('deciding on release', () => {
  const at = (dy: number, velocity = 0) =>
    shouldCommit({ dy, playerHeight: 220, velocity })

  it('commits once the movement is a quarter of the picture', () => {
    // A share of the player, not of the journey: a quarter of the picture is a
    // movement the hand can feel, while a quarter of the way to the corner is
    // most of the screen and far too much to ask for.
    expect(at(220 * COMMIT_FRACTION)).toBe(true)
    expect(at(220 * COMMIT_FRACTION - 1)).toBe(false)
  })

  it('commits a flick that never travelled far', () => {
    // A flick is an unambiguous statement. Without this it springs back, which
    // reads as the gesture having been missed rather than declined.
    expect(at(10, COMMIT_VELOCITY)).toBe(true)
  })

  it('springs back from a slow, short drag', () => {
    expect(at(20, 50)).toBe(false)
  })

  it('does not commit on a drag pulled back up', () => {
    expect(at(0, -900)).toBe(false)
  })

  it('refuses rather than dividing when nothing has been measured', () => {
    expect(shouldCommit({ dy: 500, playerHeight: 0, velocity: 0 })).toBe(false)
  })
})

describe('velocity', () => {
  it('is pixels per second', () => {
    expect(velocityOf(140, 200)).toBeCloseTo(700)
  })

  it('is zero when no time passed, rather than infinite', () => {
    // Two pointer events in the same millisecond is ordinary on a fast screen,
    // and an infinite velocity would commit every one of them.
    expect(velocityOf(40, 0)).toBe(0)
  })
})

describe('the rectangle in flight', () => {
  const full = { top: 56, left: 0, width: 390, height: 220 }
  const mini = { top: 700, left: 16, width: 358, height: 72 }

  it('is the start at zero and the end at one', () => {
    expect(lerpRect(full, mini, 0)).toEqual(full)
    expect(lerpRect(full, mini, 1)).toEqual(mini)
  })

  it('moves every edge together', () => {
    // All four, or the player changes shape on the way rather than travelling.
    const half = lerpRect(full, mini, 0.5)
    expect(half).toEqual({ top: 378, left: 8, width: 374, height: 146 })
  })

  it('clamps rather than overshooting', () => {
    // A flick can report a progress past one before the release is handled, and
    // a rect beyond the corner would be visible for that frame.
    expect(lerpRect(full, mini, 1.5)).toEqual(mini)
    expect(lerpRect(full, mini, -1)).toEqual(full)
  })
})
