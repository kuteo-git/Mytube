import { describe, expect, it } from 'vitest'
import {
  BAR_HEIGHT,
  BOTTOM_NAV_HEIGHT,
  MINI_HEIGHT,
  MINI_WIDTH,
  classifyGesture,
  dragProgress,
  fullRectMobile,
  lerpRect,
  miniRectDesktop,
  miniRectMobile,
  shouldCommit,
} from './player-geometry'

describe('miniRectDesktop', () => {
  it('sits 16px from the bottom-right corner at 1440x900', () => {
    expect(miniRectDesktop(1440, 900)).toEqual({
      top: 900 - 16 - MINI_HEIGHT,
      left: 1440 - 16 - MINI_WIDTH,
      width: MINI_WIDTH,
      height: MINI_HEIGHT,
    })
  })

  it('keeps the same margins on a 1920x1080 display', () => {
    const r = miniRectDesktop(1920, 1080)
    expect(1920 - (r.left + r.width)).toBe(16)
    expect(1080 - (r.top + r.height)).toBe(16)
  })

  it('still fits on a 1280x720 laptop screen', () => {
    const r = miniRectDesktop(1280, 720)
    expect(r.top).toBeGreaterThan(0)
    expect(r.left).toBeGreaterThan(0)
  })
})

describe('miniRectMobile', () => {
  it('rests on top of the bottom navigation rather than over it', () => {
    const r = miniRectMobile(390, 844)
    // The bar's bottom edge is exactly the navigation's top edge.
    expect(r.top + r.height).toBe(844 - BOTTOM_NAV_HEIGHT)
    expect(r.height).toBe(BAR_HEIGHT)
  })

  it('spans the full width of the viewport', () => {
    const r = miniRectMobile(390, 844)
    expect(r.left).toBe(0)
    expect(r.width).toBe(390)
  })
})

describe('fullRectMobile', () => {
  it('is a 16:9 band pinned under the top bar', () => {
    const r = fullRectMobile(390)
    expect(r.top).toBe(56)
    expect(r.width).toBe(390)
    expect(r.height).toBe(Math.round((390 * 9) / 16))
  })
})

describe('lerpRect', () => {
  const from = { top: 0, left: 0, width: 100, height: 100 }
  const to = { top: 200, left: 400, width: 50, height: 20 }

  it('returns the start rect at p = 0', () => {
    expect(lerpRect(from, to, 0)).toEqual(from)
  })

  it('returns the end rect at p = 1', () => {
    expect(lerpRect(from, to, 1)).toEqual(to)
  })

  it('is halfway on every axis at p = 0.5', () => {
    expect(lerpRect(from, to, 0.5)).toEqual({ top: 100, left: 200, width: 75, height: 60 })
  })

  it('clamps out-of-range progress instead of overshooting', () => {
    expect(lerpRect(from, to, 2)).toEqual(to)
    expect(lerpRect(from, to, -1)).toEqual(from)
  })
})

describe('classifyGesture', () => {
  it('treats movement inside the slop as a tap', () => {
    expect(classifyGesture(0, 0)).toBe('tap')
    expect(classifyGesture(9, -9)).toBe('tap')
  })

  it('ignores mostly-horizontal movement so scrubbing still works', () => {
    expect(classifyGesture(40, 10)).toBe('ignore')
    expect(classifyGesture(-40, 30)).toBe('ignore')
  })

  it('ignores upward drags', () => {
    expect(classifyGesture(0, -40)).toBe('ignore')
  })

  it('only a deliberate downward drag minimises', () => {
    expect(classifyGesture(0, 40)).toBe('drag')
    expect(classifyGesture(10, 40)).toBe('drag')
  })
})

describe('dragProgress', () => {
  it('reaches 1 at 35% of the player height', () => {
    expect(dragProgress(70, 200)).toBe(1)
  })

  it('is proportional below the threshold', () => {
    expect(dragProgress(35, 200)).toBeCloseTo(0.5)
  })

  it('clamps at both ends', () => {
    expect(dragProgress(1000, 200)).toBe(1)
    expect(dragProgress(-50, 200)).toBe(0)
  })

  it('does not divide by a zero height', () => {
    expect(dragProgress(50, 0)).toBe(0)
  })
})

describe('shouldCommit', () => {
  it('commits on distance alone', () => {
    expect(shouldCommit(1, 0)).toBe(true)
  })

  it('commits on a fast flick that covered little distance', () => {
    expect(shouldCommit(0.2, 0.9)).toBe(true)
  })

  it('springs back on a short slow drag', () => {
    expect(shouldCommit(0.2, 0.1)).toBe(false)
  })
})
