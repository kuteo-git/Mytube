/**
 * The only place in the player allowed to move a playhead.
 *
 * ## Why this exists
 *
 * A `<video>` element is built for a file with an index. The live mux is not
 * one: `/api/videos/{id}/remux` answers `Accept-Ranges: none`, its body is an
 * fMP4 down a pipe with no `sidx`, and the stream answer says so —
 * `sources.remux.seekable === false`.
 *
 * A browser told to seek such a stream does not refuse and does not report
 * anything. It accepts the number and then waits, buffering forward, until the
 * bytes for that timestamp happen to arrive — which for an unindexed stream
 * means streaming the whole way there. On a video left at 5:36 that is minutes
 * of blank picture with no error anywhere: not in the console, not in the
 * gateway log, not in ingest's. The download running beside it finishes in a
 * median of thirteen seconds, so it always looked like "you have to wait for it
 * to download before it plays".
 *
 * CLAUDE.md §4 has carried the rule for weeks — *"A stream reported
 * `seekable: false` must never be seeked"* — and it was enforced at three of
 * the five places that write `currentTime`. That is the shape of this whole
 * class of bug: the rule lives in the author's head, so each new call site is a
 * fresh chance to forget it, and forgetting is silent.
 *
 * So it stops being a rule and becomes a function. `player-seek.guard.test.ts`
 * keeps it that way by refusing any other `currentTime` assignment in the
 * feature.
 *
 * ## What to do instead of seeking
 *
 * Nothing here retries or works around a refusal, because the workaround is not
 * a seek: a stream that cannot be moved must be *opened* where it is wanted
 * (`sourceURL(tier, mark, audioStart)`), so its zero is already the mark. The
 * caller is the only one that knows whether that is possible, so the caller is
 * told what happened and decides.
 */

/**
 * All this needs of a tier, so nothing here depends on the player.
 *
 * Structural, so `Tier` satisfies it without importing anything.
 */
export interface SeekableSource {
  seekable: boolean
}

export type SeekOutcome =
  /** The playhead moved. */
  | 'seeked'
  /** The stream has no index; the caller must reopen it at the mark instead. */
  | 'refused-not-seekable'
  /** The element threw — usually not ready yet. Nothing was changed. */
  | 'refused-by-element'
  /** There was no element to move. */
  | 'no-element'

/**
 * Move `el` to `seconds`, unless the source it is playing cannot be moved.
 *
 * `seconds` is in the element's own frame, not the video's. A muxed stream
 * opened at ten minutes calls that position zero, and converting from absolute
 * to element time — subtracting the offset — belongs to the caller, which is
 * the only place that knows the offset.
 *
 * An unknown tier (`undefined`) is allowed through. It is unknown only before
 * the first source has been chosen, where the local file is the ordinary case,
 * and refusing there would break resuming a downloaded video in order to
 * protect a stream that is not playing yet.
 */
export function seekElement(
  el: HTMLVideoElement | null | undefined,
  tier: SeekableSource | undefined,
  seconds: number,
): SeekOutcome {
  if (!el) return 'no-element'
  if (tier?.seekable === false) return 'refused-not-seekable'

  // Negative marks are the caller's arithmetic showing through: an absolute
  // position minus an offset larger than it. The viewer asked to go somewhere,
  // and the start of the stream is the nearest place to it.
  const target = Math.max(0, seconds)

  try {
    el.currentTime = target
  } catch {
    // Some browsers throw on a seek the element is not ready for. Not something
    // the caller can act on, and not a reason to take down a render.
    return 'refused-by-element'
  }
  return 'seeked'
}
