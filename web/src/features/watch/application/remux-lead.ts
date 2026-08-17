/**
 * How far ahead of the viewer the next muxed stream is opened.
 *
 * A pure decision, so it lives here rather than inside the player: it is the
 * one piece of the tier machinery that can be reasoned about — and tested —
 * without a video element, two layers and a clock.
 */

/**
 * How far ahead of the playhead a muxed stream is opened.
 *
 * It begins wherever it is asked to and takes a while to get there, so the mark
 * has to be further ahead than the preparation takes — otherwise playback has
 * already passed it by the time the stream is ready, and the handover either
 * rewinds or never matches.
 *
 * How long it takes depends on the video, which is what made the first guess
 * wrong. Measured on this library: about 4.4s for a five-minute video, but
 * 10.8s for a seventy-eight-minute one — 2.8s of that resolving and the rest
 * inside ffmpeg. Twenty seconds clears the longest case measured with room to
 * spare, at the cost of the picture staying low for that long.
 */
const REMUX_PREPARE_LEAD_SECONDS = 20

/**
 * The same, for the climb back to full resolution after a seek.
 *
 * Shorter because by then the unknown is known. Twenty seconds covers the first
 * climb of a video whose network behaviour has not been seen yet; after a seek
 * the stream has already been opened at least once and the cost is measured —
 * about 3 seconds to reopen at a new mark, since the URLs are already resolved.
 * Five leaves room for that without asking the viewer to watch a third of a
 * minute of the low rendition every time they move the scrub bar.
 */
const REMUX_SEEK_LEAD_SECONDS = 5

/**
 * Added to the *measured* preparation time to get the next lead.
 *
 * The two constants above are opening guesses for a video nothing is known
 * about, and the numbers in their comments are the reason a guess cannot be
 * right: 4.4s for a five-minute video against 10.8s for a seventy-eight-minute
 * one. Twenty seconds covers the long case by being far too much for the short
 * one, which is thirty extra seconds of 360p on most of this library.
 *
 * So the first climb of a video guesses, and every climb after it uses what the
 * previous one actually took. The margin is what keeps a stream that took 4.4s
 * once and 6s the next time from landing late.
 */
const REMUX_LEAD_MARGIN_SECONDS = 4

/** Bounds on the adaptive lead, so one freak measurement cannot set it. */
const REMUX_MIN_LEAD_SECONDS = 5
const REMUX_MAX_LEAD_SECONDS = 30

/**
 * How many times a climb may be reopened at a fresh mark before auto gives up
 * on the tier.
 *
 * Being late does not mean the tier is bad, it means the mark was wrong — so a
 * late climb is reopened rather than counted against the three strikes that
 * switch 1080p off for the video. That was the trap: preparation takes about as
 * long as the lead allows, so on a long video every climb was late, three late
 * climbs turned the tier off, and pinning 1080p by hand was the only way to see
 * it.
 *
 * Bounded all the same, because a reopen costs an ffmpeg. Three is generous
 * given the lead corrects itself from the measurement each time — a mark that
 * was late once is unlikely to be late again with the real number behind it.
 */
export const MAX_CLIMB_REOPENS = 3

/**
 * How far ahead of the viewer to open the next muxed stream.
 *
 * `measuredMs` is how long the previous one took from claim to ready, undefined
 * before any has been. Once there is a real number it replaces both guesses,
 * including the post-seek one: a reopen of an already-resolved stream is exactly
 * the case that number was measured on.
 */
export function remuxLead(measuredMs: number | undefined, afterSeek: boolean): number {
  if (measuredMs === undefined) {
    return afterSeek ? REMUX_SEEK_LEAD_SECONDS : REMUX_PREPARE_LEAD_SECONDS
  }
  const wanted = measuredMs / 1000 + REMUX_LEAD_MARGIN_SECONDS
  return Math.min(REMUX_MAX_LEAD_SECONDS, Math.max(REMUX_MIN_LEAD_SECONDS, wanted))
}
