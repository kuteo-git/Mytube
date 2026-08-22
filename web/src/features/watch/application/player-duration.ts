/**
 * How long the video is, as the bar should draw it.
 *
 * Pulled out of the player because it is a decision with three sources and a
 * history of getting them in the wrong order — and because it can then be
 * checked without a DOM.
 */

/** A broadcast's rewindable window, as the element reports it. */
export interface LiveWindow {
  start: number
  end: number
}

/**
 * Pick a length.
 *
 * The element wins whenever it has said anything, and that is the correction:
 * it used to be refused for anything that was not the file on disk. The rule
 * came from the muxed tier, which declares no total length because its header
 * only says how much has been muxed so far — and that tier no longer exists.
 * HLS states the real length in the playlist, so refusing it left a video whose
 * catalogue row has no duration reading **0:00** for as long as it streamed.
 *
 * The catalogue is the fallback for the moment before metadata arrives, which
 * is what it was always good for.
 */
export function playbackDuration(opts: {
  /** `el.duration`, or 0 before metadata has arrived. */
  elementDuration: number
  /** The catalogue's length. Often 0 — a flat listing carries none. */
  catalogueDuration: number
  /** Set only while a broadcast is on air. */
  liveWindow?: LiveWindow | null
  isLive?: boolean
  /**
   * Where the stream's zero sits in the video. Always 0 today; the parameter
   * remains so that reintroducing an offset cannot silently draw a half-watched
   * film as barely begun.
   */
  offsetSeconds?: number
}): number {
  const { elementDuration, catalogueDuration, liveWindow, isLive, offsetSeconds = 0 } = opts
  // A broadcast has no total, only a window, and that window moves.
  if (isLive) return liveWindow?.end ?? 0
  if (offsetSeconds === 0 && elementDuration > 0) return elementDuration
  return catalogueDuration
}
