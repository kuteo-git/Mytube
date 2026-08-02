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

/**
 * Desktop miniplayer: 16:9, tucked into the bottom-right corner, sized against
 * the viewport between a floor and a ceiling.
 *
 * The floor is where the controls stop fitting — play, volume and captions
 * begin to crowd each other below it. The ceiling is where it stops being a
 * corner window: past about a third of the width it is no longer something the
 * page continues behind, it is the page.
 */
export const MINI_MIN_WIDTH = 400
export const MINI_MAX_WIDTH = 560
export const MINI_WIDTH_FRACTION = 0.32
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
  const width = clamp(viewportWidth * MINI_WIDTH_FRACTION, MINI_MIN_WIDTH, MINI_MAX_WIDTH)
  const height = Math.round((width * 9) / 16)
  return {
    top: viewportHeight - MINI_MARGIN - height,
    left: viewportWidth - MINI_MARGIN - width,
    width,
    height,
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

export function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}
