import { describe, expect, it } from 'vitest'
import {
  BAR_HEIGHT,
  BOTTOM_NAV_HEIGHT,
  MINI_MAX_WIDTH,
  MINI_MIN_WIDTH,
  MINI_WIDTH_FRACTION,
  fullRectMobile,
  miniRectDesktop,
  miniRectMobile,
} from './player-geometry'

describe('miniRectDesktop', () => {
  it('takes its share of the viewport at 1440x900', () => {
    const r = miniRectDesktop(1440, 900)
    expect(r.width).toBe(1440 * MINI_WIDTH_FRACTION)
    expect(r).toEqual({
      top: 900 - 16 - r.height,
      left: 1440 - 16 - r.width,
      width: r.width,
      height: r.height,
    })
  })

  it('stops shrinking at the floor, where the controls stop fitting', () => {
    expect(miniRectDesktop(1024, 640).width).toBe(MINI_MIN_WIDTH)
    expect(miniRectDesktop(800, 600).width).toBe(MINI_MIN_WIDTH)
  })

  it('stops growing at the ceiling, so it stays a corner window', () => {
    expect(miniRectDesktop(1920, 1080).width).toBe(MINI_MAX_WIDTH)
    expect(miniRectDesktop(2560, 1440).width).toBe(MINI_MAX_WIDTH)
  })

  it('is 16:9 at every size', () => {
    for (const width of [800, 1280, 1440, 1920, 2560]) {
      const r = miniRectDesktop(width, 900)
      expect(r.height).toBe(Math.round((r.width * 9) / 16))
    }
  })

  it('keeps the same margins whatever the size', () => {
    for (const [vw, vh] of [
      [1280, 720],
      [1920, 1080],
      [2560, 1440],
    ]) {
      const r = miniRectDesktop(vw, vh)
      expect(vw - (r.left + r.width)).toBe(16)
      expect(vh - (r.top + r.height)).toBe(16)
    }
  })

  it('still fits on a small laptop screen', () => {
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
  it('is a 16:9 band at the very top of the screen', () => {
    // No longer under a top bar of ours: the watch screen on a phone has no
    // header, being a screen of its own rather than a page inside the app's
    // chrome. On a device with no notch this really is the top edge.
    const r = fullRectMobile(390)
    expect(r.top).toBe(0)
    expect(r.width).toBe(390)
    expect(r.height).toBe(Math.round((390 * 9) / 16))
  })

  it('clears the status bar where there is one', () => {
    // Otherwise the picture sits under the clock: viewport-fit=cover means the
    // page owns the notch, so it has to allow for it.
    expect(fullRectMobile(390, 47).top).toBe(47)
  })
})
