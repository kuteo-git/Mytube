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

import { MOBILE_BREAKPOINT } from '@/features/watch/application/player-geometry'

export type Direction = 'push' | 'pop'

function historyIndex(): number {
  return (window.history.state as { idx?: number } | null)?.idx ?? 0
}

let lastIndex = typeof window === 'undefined' ? 0 : historyIndex()

/**
 * Say which way the screen is about to go.
 *
 * Told rather than inferred, because it has to be known *before* the
 * navigation: CSS reads it off the root while the transition is running, and
 * the history index only moves once the navigation has already happened. An
 * earlier version worked it out afterwards in an effect and was a frame late,
 * which is the whole animation.
 *
 * The index is still tracked, for the one case nothing here can wrap: the
 * browser's own back gesture does not go through any link.
 */
export function markDirection(direction: Direction): Direction {
  lastIndex = historyIndex()
  document.documentElement.dataset.nav = direction
  return direction
}

/**
 * Which way a navigation that has already happened went.
 *
 * For the browser's back gesture and the hardware button, which cannot be
 * wrapped in a transition at all — there is no callback to put the DOM change
 * inside. The screen changes without animating; this at least leaves the root
 * describing what happened rather than describing the last thing that did.
 */
export function observeDirection(): Direction {
  const now = historyIndex()
  const direction: Direction = now < lastIndex ? 'pop' : 'push'
  lastIndex = now
  document.documentElement.dataset.nav = direction
  return direction
}

/**
 * Whether to animate between screens at all.
 *
 * Two conditions, and the second is a decision rather than a capability.
 *
 * `startViewTransition` is the only way to keep the outgoing screen on screen
 * while the incoming one arrives: React has already unmounted it by the time
 * any CSS could run. Safari has it from 18, Chrome from 111. Where it is
 * missing the navigation simply happens.
 *
 * **Phones only.** A screen sliding in from the right is how a phone says one
 * thing is inside another, and it says that because a phone shows one screen at
 * a time. A desktop shows the sidebar the whole way through, so the same
 * animation slides the *content* across while the navigation beside it sits
 * still — which reads as the page having been shoved rather than entered. It
 * was applied everywhere at first and looked wrong on a wide window
 * immediately.
 *
 * The same 700px the rest of the app uses, read at the moment of the press
 * rather than subscribed to: a window resized mid-navigation is not a case
 * worth code.
 */
export function canAnimatePages(): boolean {
  if (typeof document === 'undefined') return false
  if (!('startViewTransition' in document)) return false
  return window.innerWidth < MOBILE_BREAKPOINT
}
