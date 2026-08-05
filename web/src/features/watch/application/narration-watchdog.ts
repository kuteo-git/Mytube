/**
 * Whether narration has quietly stopped and should be restarted.
 *
 * The three faults this was written after all had the same shape: something
 * decided not to speak, and nothing ever revisited that decision. A cue skipped
 * for want of a translation was skipped for the rest of the video; a cursor left
 * past the playhead by a layer swap stayed there. In every case the viewer's own
 * fix was to seek, because a seek is the one thing that puts the cursor back.
 *
 * Each of those has been fixed at its own root. This is the net underneath —
 * narrowly defined, so it can only ever catch a state that is unambiguously
 * wrong, and so that a fourth fault of the same shape is a few silent seconds
 * rather than a video with no narration at all.
 *
 * Deliberately not a general reconciliation sweep. CLAUDE.md §4 argues against
 * those and is right: a sweep that re-asserts everything is a second place for
 * every rule to live, and its mistakes are bulk and silent. This asks one
 * question about one thing.
 */

/**
 * Seconds of playback with nothing said and nothing queued before it counts as
 * stalled.
 *
 * Longer than any ordinary gap between subtitles — people stop talking — and
 * short enough that the viewer has not yet given up and reached for the seek
 * bar. Cues also have to be *available* to be late, which is what keeps a
 * silent stretch of film from tripping this.
 */
export const STALL_SECONDS = 4

export interface NarrationHealth {
  /** Reading aloud is switched on and this video has cues to read. */
  wanted: boolean
  playing: boolean
  /** Clips currently on the audio timeline. */
  scheduled: number
  /** Cue index the pump will commit next. */
  cursor: number
  /** Cue index the playhead is at, or the next one due. */
  cursorAtPlayhead: number
  /** Seconds of playback since anything was last placed. */
  silentFor: number
}

export function hasStalled({
  wanted,
  playing,
  scheduled,
  cursor,
  cursorAtPlayhead,
  silentFor,
}: NarrationHealth): boolean {
  if (!wanted || !playing) return false
  // Something is queued, so it is working — a long clip is not a stall.
  if (scheduled > 0) return false
  // The cursor is ahead of the playhead: there is a line still to come and the
  // pump is simply waiting for its moment. That is the ordinary state between
  // two subtitles and must never be restarted, or narration would be reset
  // several times a minute for doing exactly the right thing.
  if (cursor > cursorAtPlayhead) return false
  return silentFor >= STALL_SECONDS
}
