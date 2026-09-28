/**
 * Scrub-preview stills: which part of which sheet stands for a moment.
 *
 * YouTube publishes previews as sprite sheets — one image carrying a grid of
 * small stills — and the gateway copies them under the media root and answers
 * with their geometry. Turning a moment into a crop is arithmetic, so it lives
 * here rather than in the component: the mobile client does the same sum in
 * Kotlin, and a rule written twice in two languages is a rule that agrees until
 * one copy is fixed.
 */

/** What `GET /api/videos/{id}/storyboard` answers. */
export type Storyboard = {
  /** The size of one still, in pixels. */
  tileWidth: number
  tileHeight: number
  /** The grid inside each sheet. */
  rows: number
  columns: number
  /** How much of the video one still stands for. */
  intervalSeconds: number
  /** The sheets in order, as paths relative to the media root. */
  sprites: string[]
}

/** Where to find one still: which sheet, and how far into it. */
export type StoryboardFrame = {
  /** The sheet's reference, still needing `mediaURL`. */
  sprite: string
  /**
   * How far into the sheet the still sits, in pixels, measured to its top left.
   *
   * Positive, and a caller draws it by shifting the sheet the other way — a
   * `background-position` of `-x -y`, or a negated offset in a layout. Returned
   * as a distance rather than as CSS so the mobile client can use the same
   * numbers, where there is no such thing as a background position.
   */
  x: number
  y: number
}

/**
 * The still to draw for a moment, or nothing if there is none to draw.
 *
 * Nothing rather than a blank frame for a storyboard that cannot be read:
 * a caller has to decide what to show in that case anyway — both clients keep
 * drawing the time readout they already drew — and a frame pointing at sheet
 * zero would put the opening shot under every moment of the video, which looks
 * like a preview that works and is wrong everywhere but the start.
 */
export function storyboardFrame(
  board: Storyboard | undefined,
  seconds: number,
): StoryboardFrame | undefined {
  if (!board) return undefined
  const { rows, columns, intervalSeconds, sprites } = board
  if (rows <= 0 || columns <= 0 || intervalSeconds <= 0 || sprites.length === 0) return undefined
  if (!Number.isFinite(seconds)) return undefined

  const perSheet = rows * columns
  // Clamped at both ends, and the far end is the one that matters.
  //
  // The last sheet is usually only partly filled — six sheets of 25 hold 150
  // slots for a video with 128 stills — so a moment near the end lands in a
  // cell that exists while the one after it does not. Clamping to the last cell
  // of the last sheet keeps the preview drawn all the way to the end of the
  // bar; without it the final seconds of every video have no picture, which
  // reads as the feature failing exactly where somebody is looking hardest.
  const frame = Math.min(
    Math.max(Math.floor(Math.max(seconds, 0) / intervalSeconds), 0),
    sprites.length * perSheet - 1,
  )

  const sheet = Math.floor(frame / perSheet)
  const within = frame % perSheet
  return {
    sprite: sprites[sheet],
    // Row-major, which is how YouTube lays a sheet out: the second still is to
    // the right of the first, not under it.
    x: (within % columns) * board.tileWidth,
    y: Math.floor(within / columns) * board.tileHeight,
  }
}
