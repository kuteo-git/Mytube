import { clamp } from './player-geometry'

/**
 * The watch screen as a layer over the page you came from, on a phone.
 *
 * Not a page beside the others: it has no header and no bottom bar, and behind
 * it the tab you were on is still there. Pulling the player down does not
 * "navigate to Home", it puts the layer away and reveals whatever was
 * underneath — History if you came from History, Saved if you came from Saved.
 */

/**
 * How much of the screen the drag takes to clear the watch layer away.
 *
 * A tenth, and deliberately a small number: everything except the player goes
 * almost at once, so the rest of the movement is a picture travelling across a
 * page that is already fully visible rather than a page slowly dissolving. It
 * is a named constant because it is the one figure here that has to be judged
 * with a thumb rather than argued about — expect to move it.
 */
export const DISMISS_FADE_FRACTION = 0.1

/**
 * How far the layer has faded, from 0 (untouched) to 1 (gone).
 *
 * Measured against the viewport rather than against the player or the journey:
 * the number is about how far a hand has moved on a screen, and a hand does not
 * know how tall the player is.
 */
export function dismissFade(dy: number, viewportHeight: number): number {
  const over = viewportHeight * DISMISS_FADE_FRACTION
  if (!(over > 0)) return 0
  return clamp(dy / over, 0, 1)
}

/**
 * What the watch layer and the chrome behind it should be drawn at.
 *
 * One number produces both, so they cannot drift apart: the layer going out and
 * the bottom bar coming in are the same movement seen from two sides. A gap
 * between them would show as the navigation arriving late, after the page it
 * belongs to.
 */
export function layerOpacity(fade: number): number {
  return clamp(1 - fade, 0, 1)
}
