import { afterEach, describe, expect, it, vi } from 'vitest'

import { canAnimatePages, markDirection } from './page-transition'

/**
 * Screens slide on a phone and change instantly on a desktop.
 *
 * The animation says one screen is *inside* another, and a phone can say that
 * because it shows one screen at a time. A desktop keeps the sidebar on screen
 * throughout, so the same animation slides the content across while the
 * navigation beside it sits still — which reads as the page having been shoved
 * rather than entered. It was applied everywhere at first and looked wrong on a
 * wide window immediately.
 */

function atWidth(px: number) {
  Object.defineProperty(window, 'innerWidth', { value: px, configurable: true })
}

afterEach(() => {
  vi.unstubAllGlobals()
  atWidth(1024)
  delete document.documentElement.dataset.nav
})

describe('when screens animate', () => {
  it('does on a phone-width window', () => {
    vi.stubGlobal('document', Object.assign(document, { startViewTransition: () => {} }))
    atWidth(390)
    expect(canAnimatePages()).toBe(true)
  })

  it('does not on a desktop-width window', () => {
    vi.stubGlobal('document', Object.assign(document, { startViewTransition: () => {} }))
    atWidth(1200)
    expect(canAnimatePages()).toBe(false)
  })

  /** 700 is the same breakpoint the rest of the app splits on. */
  it('changes at the breakpoint the app already uses', () => {
    vi.stubGlobal('document', Object.assign(document, { startViewTransition: () => {} }))
    atWidth(699)
    expect(canAnimatePages()).toBe(true)
    atWidth(700)
    expect(canAnimatePages()).toBe(false)
  })

  /**
   * Without the API there is no way to keep the outgoing screen on screen —
   * React has unmounted it before any CSS could reach it — so the navigation
   * simply happens, which is what it always did.
   */
  it('does not where the browser has no view transitions', () => {
    atWidth(390)
    // @ts-expect-error — removing an optional API the way an older browser has it
    delete document.startViewTransition
    expect(canAnimatePages()).toBe(false)
  })
})

describe('which way the screen goes', () => {
  /**
   * Told rather than inferred: CSS reads it off the root while the transition
   * is running, and the history index only moves once the navigation has
   * already happened.
   */
  it('is written on the root before the navigation', () => {
    markDirection('push')
    expect(document.documentElement.dataset.nav).toBe('push')
    markDirection('pop')
    expect(document.documentElement.dataset.nav).toBe('pop')
  })
})
