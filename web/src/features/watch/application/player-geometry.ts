/**
 * Pure geometry and gesture arithmetic for the player host.
 *
 * Deliberately free of React and of the DOM. Every number the miniplayer depends
 * on is decided here so it can be tested for real: jsdom has no layout engine, so
 * `getBoundingClientRect()` there returns zeroes and `IntersectionObserver` does
 * not exist. Testing the animation inside jsdom would only test the mocks. What
 * *can* be tested honestly is the arithmetic, so the arithmetic lives alone.
 */

/**
 * A rectangle in whichever coordinate space the caller is working in.
 *
 * Only the four values that position an element. The old shape also carried
 * `bottom`/`right`, which were derivable from the rest and had to be kept in
 * sync by hand at every construction site.
 */
export interface ViewRect {
  top: number
  left: number
  width: number
  height: number
}

/** Desktop miniplayer: 400x225 (16:9) tucked into the bottom-right corner. */
export const MINI_WIDTH = 400
export const MINI_HEIGHT = 225
export const MINI_MARGIN = 16

/** Mobile miniplayer: a full-width bar, the shape the YouTube app uses. */
export const BAR_HEIGHT = 72
/** Height of the mobile bottom navigation the bar has to sit on top of. */
export const BOTTOM_NAV_HEIGHT = 56
/** Height of the top bar the mobile full-size player is pinned beneath. */
export const TOP_BAR_HEIGHT = 56

/** Viewport width below which the app uses its mobile shell. */
export const MOBILE_BREAKPOINT = 700

export function miniRectDesktop(viewportWidth: number, viewportHeight: number): ViewRect {
  return {
    top: viewportHeight - MINI_MARGIN - MINI_HEIGHT,
    left: viewportWidth - MINI_MARGIN - MINI_WIDTH,
    width: MINI_WIDTH,
    height: MINI_HEIGHT,
  }
}

/**
 * The bar rests directly on the bottom navigation rather than overlapping it.
 * Covering the navigation would mean the miniplayer eats the way out of it.
 */
export function miniRectMobile(
  viewportWidth: number,
  viewportHeight: number,
  navHeight: number = BOTTOM_NAV_HEIGHT,
): ViewRect {
  return {
    top: viewportHeight - navHeight - BAR_HEIGHT,
    left: 0,
    width: viewportWidth,
    height: BAR_HEIGHT,
  }
}

/** The full-size player on mobile: pinned under the top bar, 16:9, edge to edge. */
export function fullRectMobile(viewportWidth: number): ViewRect {
  return {
    top: TOP_BAR_HEIGHT,
    left: 0,
    width: viewportWidth,
    height: Math.round((viewportWidth * 9) / 16),
  }
}

export function lerpRect(from: ViewRect, to: ViewRect, p: number): ViewRect {
  const t = clamp(p, 0, 1)
  return {
    top: from.top + (to.top - from.top) * t,
    left: from.left + (to.left - from.left) * t,
    width: from.width + (to.width - from.width) * t,
    height: from.height + (to.height - from.height) * t,
  }
}

export function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

/* ---------------------------------------------------------------- gestures */

export type GestureVerdict = 'tap' | 'ignore' | 'drag'

/** Movement below this is a tap, not a drag: fingers are never perfectly still. */
export const TAP_SLOP = 10
/** Fraction of the player's height a drag must cover to commit on distance alone. */
export const COMMIT_FRACTION = 0.35
/** Downward speed, px/ms, that commits regardless of distance covered. */
export const COMMIT_VELOCITY = 0.5

/**
 * Which of the three things a pointer movement on the player surface is.
 *
 * The axis lock is what keeps this from fighting the controls: a horizontal
 * movement is somebody scrubbing, and an upward one is not a request to put the
 * video away. Only a deliberate downward drag minimises.
 */
export function classifyGesture(dx: number, dy: number): GestureVerdict {
  if (Math.abs(dx) < TAP_SLOP && Math.abs(dy) < TAP_SLOP) return 'tap'
  if (Math.abs(dx) > Math.abs(dy)) return 'ignore'
  if (dy < 0) return 'ignore'
  return 'drag'
}

export function dragProgress(dy: number, playerHeight: number): number {
  if (playerHeight <= 0) return 0
  return clamp(dy / (playerHeight * COMMIT_FRACTION), 0, 1)
}

/**
 * Distance *or* speed commits.
 *
 * The velocity branch is not a refinement. A short quick flick is how people
 * actually dismiss things on a phone, and on distance alone every one of those
 * springs back — which reads as the app ignoring them.
 */
export function shouldCommit(progress: number, velocity: number): boolean {
  return progress >= 1 || velocity > COMMIT_VELOCITY
}
