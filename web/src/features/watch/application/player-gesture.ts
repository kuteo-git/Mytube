import { type ViewRect, clamp } from './player-geometry'

/**
 * Dragging the full-size player down into the corner.
 *
 * This gesture existed once and was removed on 2026-08-03 as useless, and the
 * charter is right about that — but the reason is worth being precise about,
 * because it is the reason this version is different. The old one *minimised in
 * place*: it left the player in the corner of the very page it was already on,
 * so the gesture bought nothing. Pulling the player down is a request to go and
 * look at something else, and it is only useful if it takes you there.
 *
 * On a phone that falls out for free. Leaving the watch page is what turns the
 * player into the bar (CLAUDE.md §8b), so the gesture does not need a minimised
 * state of its own — it navigates, and the bar is what the player becomes.
 * What is left here is the part that has to feel right: following the finger.
 */

/**
 * Movement before a drag is admitted, in pixels.
 *
 * A tap is what shows and hides the controls on touch, and a finger never lands
 * perfectly still. Below this, nothing has happened yet — which is what keeps
 * every tap on the picture from nudging the player.
 */
export const DRAG_SLOP = 12

/**
 * How far down counts as "put it away", as a fraction of the player's height.
 *
 * A quarter, so the decision is made early enough that the rest of the movement
 * feels like the app agreeing rather than the viewer still asking.
 */
export const COMMIT_FRACTION = 0.25

/**
 * Downward speed that commits regardless of distance, in pixels per second.
 *
 * A flick is an unambiguous statement and should not have to travel far to be
 * heard. Without it, a fast short flick springs back — which reads as the
 * gesture having been missed rather than declined.
 */
export const COMMIT_VELOCITY = 700

/**
 * Whether a pointer movement is a vertical drag rather than something else.
 *
 * Horizontal wins ties on purpose: sideways movement over a player belongs to
 * seeking and to the browser's own back gesture, and stealing an ambiguous
 * swipe from either is worse than missing an ambiguous drag.
 */
export function isVerticalDrag(dx: number, dy: number): boolean {
  if (dy <= 0) return false
  return Math.hypot(dx, dy) >= DRAG_SLOP && dy > Math.abs(dx)
}

/**
 * How far along its journey the player is, from 0 to 1.
 *
 * Measured against the distance the player actually has to travel — the gap
 * between where it sits full size and where the corner is — and NOT against its
 * own height. That was the first version's mistake and it was plainly visible:
 * a phone player is about 220px tall while the corner is some 640px away, so
 * progress reached 1 after a third of the movement and the picture arrived in
 * the corner while the finger was still in the middle of the screen. It read as
 * the player fleeing the hand that was dragging it.
 *
 * With the real distance, `lerp` puts the top edge at exactly `start + dy` —
 * the object stays under the finger because the arithmetic says so, not because
 * a constant was tuned until it looked close.
 */
export function travelProgress(dy: number, travel: number): number {
  if (!(travel > 0)) return 0
  return clamp(dy / travel, 0, 1)
}

/**
 * Whether letting go here should put the player away.
 *
 * The threshold is a share of the player's height rather than of the journey:
 * a quarter of the picture is a movement the hand can feel, while a quarter of
 * the way to the corner is most of the screen and far too much to ask.
 */
export function shouldCommit({
  dy,
  playerHeight,
  velocity,
}: {
  dy: number
  playerHeight: number
  velocity: number
}): boolean {
  if (velocity >= COMMIT_VELOCITY) return true
  if (!(playerHeight > 0)) return false
  return dy >= playerHeight * COMMIT_FRACTION
}

/**
 * Rect part-way between two, for a drag in flight.
 *
 * Linear, deliberately. An easing curve here was tried and removed: it makes
 * the player run ahead of the finger, which is the one thing a direct
 * manipulation gesture must never do. Whatever is being dragged has to stay
 * under the hand dragging it.
 */
export function lerpRect(from: ViewRect, to: ViewRect, t: number): ViewRect {
  const k = clamp(t, 0, 1)
  const mix = (a: number, b: number) => a + (b - a) * k
  return {
    top: mix(from.top, to.top),
    left: mix(from.left, to.left),
    width: mix(from.width, to.width),
    height: mix(from.height, to.height),
  }
}

/** Pixels per second, from a movement and the time it took. */
export function velocityOf(dy: number, elapsedMs: number): number {
  if (elapsedMs <= 0) return 0
  return (dy / elapsedMs) * 1000
}
