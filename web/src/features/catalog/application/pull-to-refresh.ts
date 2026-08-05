/**
 * Pull the feed down to fetch it again.
 *
 * A phone has no refresh button and should not grow one: the gesture is the
 * control everywhere else, and a page that answers it is a page that behaves
 * like the rest of the device.
 *
 * It matters more here than on most feeds. The scanner runs hourly (CLAUDE.md
 * §8b) and the ranking is frozen into a thirty-minute snapshot, so "is there
 * anything new" is a question with a real answer that the page will otherwise
 * not go and ask.
 */

/** How far the finger has to travel before letting go means anything. */
export const REFRESH_THRESHOLD = 72

/**
 * Where the indicator stops, however hard the page is pulled.
 *
 * Past the threshold there is nothing more to say — the answer is already yes —
 * and a spinner that keeps sliding down the screen invites the viewer to keep
 * pulling in case something else happens.
 */
export const MAX_PULL = 110

/**
 * How much of the finger's movement the page actually follows.
 *
 * One to one at the start, falling away as the pull grows: the page answers
 * immediately, then gets heavier, which is what tells a hand it has reached the
 * end of something. Following exactly the whole way reads as the page having
 * come loose from the screen; resisting from the first pixel reads as the
 * gesture not having been noticed.
 *
 * The curve is tuned by the only measurement that matters to a thumb — how far
 * it has to travel before letting go will do something. About 120px here, which
 * is roughly what a phone does natively, and the constant exists to hold that
 * rather than because the shape is interesting.
 */
export function pullDistance(dy: number): number {
  if (dy <= 0) return 0
  const eased = MAX_PULL * (1 - Math.exp(-dy / MAX_PULL))
  return Math.min(eased, MAX_PULL)
}

/** Whether letting go here should refetch. */
export function shouldRefresh(distance: number): boolean {
  return distance >= REFRESH_THRESHOLD
}

/**
 * How far through the gesture the indicator is, 0 to 1.
 *
 * Drives the arrow's rotation and its opacity, so the control says what
 * releasing will do *before* it is released — the difference between a gesture
 * that can be aborted and one that can only be regretted.
 */
export function pullProgress(distance: number): number {
  if (!(REFRESH_THRESHOLD > 0)) return 0
  return Math.min(distance / REFRESH_THRESHOLD, 1)
}

/**
 * Whether a downward drag from here is a pull rather than a scroll.
 *
 * Only from the very top. One pixel down the page and the same movement means
 * "scroll back up", which is a far more common thing to want — taking it would
 * make the feed feel stuck.
 */
export function canPull(scrollTop: number): boolean {
  return scrollTop <= 0
}
