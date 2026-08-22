/**
 * Which way the screen is going, so the animation can match.
 *
 * iOS pushes a screen in from the right and pops it back out to the right; the
 * two look nothing alike, and getting the direction wrong is worse than having
 * no animation at all — a screen that slides in from the left when you tapped
 * *into* something reads as having gone backwards.
 *
 * The router does not say which happened. What does is the history index:
 * `window.history.state.idx` counts up on a push and back down on a pop, and it
 * is the same number `BackBar` already reads to decide whether there is
 * anything to go back to.
 *
 * Written onto `documentElement` rather than passed through React, because what
 * consumes it is a CSS animation on the view transition's own pseudo-elements —
 * which exist outside the React tree entirely.
 */

export type Direction = 'push' | 'pop'

function historyIndex(): number {
  return (window.history.state as { idx?: number } | null)?.idx ?? 0
}

let lastIndex = typeof window === 'undefined' ? 0 : historyIndex()

/**
 * Record which way this navigation went, and hand back the direction.
 *
 * A replace navigation leaves the index unchanged — the redirect from
 * `/settings/profile` to `/profile` is one — and that is a push as far as
 * anybody watching is concerned: they arrived somewhere new.
 */
export function markDirection(): Direction {
  const now = historyIndex()
  const direction: Direction = now < lastIndex ? 'pop' : 'push'
  lastIndex = now
  document.documentElement.dataset.nav = direction
  return direction
}

/**
 * Whether this browser can animate between screens at all.
 *
 * `startViewTransition` is the only way to keep the outgoing screen on screen
 * while the incoming one arrives: React has already unmounted it by the time
 * any CSS could run. Safari has it from 18, Chrome from 111. Where it is
 * missing the navigation simply happens, which is what happens today.
 */
export function canAnimatePages(): boolean {
  return typeof document !== 'undefined' && 'startViewTransition' in document
}
