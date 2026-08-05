import { describe, expect, it } from 'vitest'
import {
  type PlacementInput,
  bridgePlacement,
  deriveMode,
  needsBridge,
  placementFor,
  resolvePin,
  toViewport,
} from './player-host'
import { BAR_HEIGHT, BOTTOM_NAV_HEIGHT, miniRectDesktop } from './player-geometry'

const base: PlacementInput = {
  mode: 'full',
  isMobile: false,
  slotDocRect: { top: 500, left: 100, width: 1280, height: 720 },
  viewport: { width: 1440, height: 900 },
  safeBottom: 0,
  safeTop: 0,
  navHeight: BOTTOM_NAV_HEIGHT,
  scrollY: 0,
}

describe('deriveMode', () => {
  it('is hidden whenever there is no video, wherever the viewer is', () => {
    expect(deriveMode(false, true, false)).toBe('hidden')
    expect(deriveMode(false, false, true)).toBe('hidden')
  })

  it('is full only on the watch page and only unpinned', () => {
    expect(deriveMode(true, true, false)).toBe('full')
  })

  it('is mini once pinned, even while still on the watch page', () => {
    expect(deriveMode(true, true, true)).toBe('mini')
  })

  it('is mini anywhere off the watch page', () => {
    expect(deriveMode(true, false, false)).toBe('mini')
    expect(deriveMode(true, false, true)).toBe('mini')
  })
})

describe('resolvePin', () => {
  it('pins when the slot scrolls out of view', () => {
    expect(resolvePin(false, false)).toEqual({ pinned: true, dismissed: false })
  })

  it('does not pin while dismissed, however far out of view the slot is', () => {
    // The whole point of the close button: scrolling one more line must not
    // bring back what was just put away.
    expect(resolvePin(false, true)).toEqual({ pinned: false, dismissed: true })
  })

  it('unpins and clears the dismissal when the slot comes back into view', () => {
    expect(resolvePin(true, true)).toEqual({ pinned: false, dismissed: false })
    expect(resolvePin(true, false)).toEqual({ pinned: false, dismissed: false })
  })

  it('pins again on the next scroll away once the dismissal has expired', () => {
    const seen = resolvePin(true, true)
    expect(resolvePin(false, seen.dismissed).pinned).toBe(true)
  })
})

describe('placementFor', () => {
  it('renders nothing when hidden', () => {
    expect(placementFor({ ...base, mode: 'hidden' })).toBeNull()
  })

  it('puts the desktop full player in document coordinates so the browser scrolls it', () => {
    const p = placementFor(base)!
    expect(p.position).toBe('absolute')
    expect(p.rect).toEqual(base.slotDocRect)
  })

  it('pins the mobile full player to the top of the screen', () => {
    // The phone's watch screen has no header of its own, so there is nothing to
    // sit beneath but the status bar — and nothing at all on a device without
    // one.
    const p = placementFor({ ...base, isMobile: true, viewport: { width: 390, height: 844 } })!
    expect(p.position).toBe('fixed')
    expect(p.rect.top).toBe(0)
    expect(p.rect.width).toBe(390)
  })

  it('leaves the notch alone where there is one', () => {
    const p = placementFor({
      ...base,
      isMobile: true,
      safeTop: 47,
      viewport: { width: 390, height: 844 },
    })!
    expect(p.rect.top).toBe(47)
  })

  it('waits in the corner rather than moving to a slot it has not measured', () => {
    // Otherwise the empty rect sends it to the top-left at zero size first, and
    // the move to the real slot becomes a second leg the viewer never asked for.
    const p = placementFor({ ...base, slotDocRect: null })!
    expect(p.position).toBe('fixed')
    expect(p.rect.width).toBe(miniRectDesktop(1440, 900).width)
  })

  it('fixes the desktop miniplayer to the bottom-right corner', () => {
    const p = placementFor({ ...base, mode: 'mini' })!
    expect(p.position).toBe('fixed')
    expect(p.rect).toEqual(miniRectDesktop(1440, 900))
  })

  it('clears the home indicator as well as the navigation', () => {
    // The navigation grows to sit above the indicator, so a bar that only knew
    // about the navigation's nominal height would land back on top of its
    // labels — the exact overlap this is meant to remove, moved up 34 pixels.
    const inset = 34
    const p = placementFor({
      ...base,
      mode: 'mini',
      isMobile: true,
      viewport: { width: 390, height: 844 },
      safeBottom: inset,
    })!
    expect(p.rect.top + p.rect.height).toBe(844 - BOTTOM_NAV_HEIGHT - inset)
  })

  it('rests on the bottom edge where there is no navigation', () => {
    // A channel and the watch layer draw their own chrome, so there is no tab
    // bar for the player to sit above. A fixed BOTTOM_NAV_HEIGHT left it
    // floating a tab bar's height over nothing — reported on the Subscriptions
    // channel screen.
    const p = placementFor({
      ...base,
      mode: 'mini',
      isMobile: true,
      navHeight: 0,
      viewport: { width: 390, height: 844 },
    })!
    expect(p.rect.top + p.rect.height).toBe(844)
  })

  it('makes the mobile miniplayer a bar above the navigation', () => {
    const p = placementFor({
      ...base,
      mode: 'mini',
      isMobile: true,
      viewport: { width: 390, height: 844 },
    })!
    expect(p.rect.height).toBe(BAR_HEIGHT)
    expect(p.rect.top + p.rect.height).toBe(844 - BOTTOM_NAV_HEIGHT)
  })

})

describe('needsBridge', () => {
  const abs = { position: 'absolute' as const, rect: base.slotDocRect!, animate: true }
  const fix = { position: 'fixed' as const, rect: base.slotDocRect!, animate: true }

  it('is required when the coordinate space changes', () => {
    expect(needsBridge(abs, fix)).toBe(true)
    expect(needsBridge(fix, abs)).toBe(true)
  })

  it('is not required within one space', () => {
    expect(needsBridge(fix, fix)).toBe(false)
    expect(needsBridge(abs, abs)).toBe(false)
  })

  it('is not required when there is nothing to move from or to', () => {
    expect(needsBridge(null, fix)).toBe(false)
    expect(needsBridge(abs, null)).toBe(false)
  })
})

describe('bridgePlacement', () => {
  it('looks identical on screen while changing space', () => {
    const from = { position: 'absolute' as const, rect: { top: 500, left: 100, width: 1280, height: 720 }, animate: true }
    const to = { position: 'fixed' as const, rect: { top: 659, left: 1024, width: 400, height: 225 }, animate: true }

    const bridge = bridgePlacement(from, to, 300)

    expect(bridge.position).toBe('fixed')
    // Document top 500 with the page scrolled 300 is viewport top 200 — the same
    // pixels the viewer was already looking at.
    expect(bridge.rect.top).toBe(200)
    expect(bridge.rect.left).toBe(100)
    expect(bridge.animate).toBe(false)
  })

  it('converts back to document coordinates in the other direction', () => {
    const from = { position: 'fixed' as const, rect: { top: 200, left: 100, width: 400, height: 225 }, animate: true }
    const to = { position: 'absolute' as const, rect: { top: 500, left: 100, width: 1280, height: 720 }, animate: true }

    expect(bridgePlacement(from, to, 300).rect.top).toBe(500)
  })
})

describe('toViewport', () => {
  it('subtracts the scroll offset from the top only', () => {
    expect(toViewport({ top: 500, left: 40, width: 10, height: 10 }, 120)).toEqual({
      top: 380,
      left: 40,
      width: 10,
      height: 10,
    })
  })
})
