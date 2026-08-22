/**
 * How a broadcast's timeline is measured.
 *
 * Pulled out of the player because the arithmetic is the whole of a real bug
 * and is worth being able to test without a DOM: a live stream declares no
 * duration, so `position / Math.max(duration, 1)` on a broadcast 26 minutes in
 * came to 155,700% — a bar painted solid red from the first second, reading
 * "25:57 / 0:00" beside it.
 *
 * What a live playlist does declare is `seekable`, and that is the only honest
 * statement of length available. Measured on two real broadcasts through this
 * server: 0..3605 on one, 0..1285 on another.
 */

/** The rewindable window, as the element reports it. */
export interface LiveWindow {
  start: number
  end: number
}

/**
 * How close to the far end still counts as watching what is happening.
 *
 * Not exact equality: the edge moves while the picture plays, so a viewer who
 * has touched nothing sits a segment or two behind it permanently. Ten seconds
 * is about two segments at the 5s target duration these playlists declare.
 */
export const LIVE_EDGE_TOLERANCE = 10

/**
 * Where the filled part of the bar ends, as a percentage.
 *
 * Measured from the window's start rather than from zero, because a broadcast
 * has no zero that can still be played — the window slides forward, and drawing
 * from zero gives a bar whose filled part shrinks while the picture advances.
 */
export function livePercent(window: LiveWindow | null, position: number): number {
  if (!window) return 0
  const span = window.end - window.start
  if (span <= 0) return 0
  return Math.min(Math.max(((position - window.start) / span) * 100, 0), 100)
}

/** Whether the viewer is watching what is happening rather than a rewind. */
export function atLiveEdge(window: LiveWindow | null, position: number): boolean {
  if (!window || window.end <= 0) return false
  return window.end - position < LIVE_EDGE_TOLERANCE
}
