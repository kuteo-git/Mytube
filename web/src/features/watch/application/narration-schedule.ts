import type { CueText } from './narration-vtt'

/** VieNeu-TTS reads slightly slow; 1.1× sounds natural. */
export const DEFAULT_SPEED = 1.1

/** ffmpeg's atempo preserves pitch, so this is fast but still clear. */
export const MAX_SPEED = 3.0

/** A cue is never given less than this, however tightly it is timed. */
export const MIN_SLOT_SECONDS = 0.1

/**
 * How long a cue's narration has before the next one is due.
 *
 * The end of the cue is not the answer. Subtitles are timed to the words on
 * screen, and the silence after them belongs to nobody: given
 *
 *     [1s → 2s]  "xin chào các bạn tôi là AI"
 *     [4s → 6s]  "các bạn khoẻ không?"
 *
 * the first line has three seconds before anything else needs to be said, not
 * one. Reading it against the one second between its own timestamps forces the
 * voice to hurry through a sentence that had all the room it needed.
 *
 * The previous version allowed the gap but capped it at twice the cue's own
 * length, which returned two seconds here — still hurried, and the cap was a
 * number with nothing behind it. There is no cap now, because a wide slot means
 * only "no need to speed up": the clip still plays at its natural pace and
 * still finishes long before the next cue. The one real constraint is not
 * running into the next line, and that is the whole formula.
 */
export function slotFor(cues: CueText[], index: number): number {
  const cue = cues[index]
  if (!cue) return MIN_SLOT_SECONDS

  const next = cues[index + 1]
  const until = next ? next.start : cue.end
  return Math.max(MIN_SLOT_SECONDS, until - cue.start)
}

/**
 * Whether a clip that became ready at `readyAt` is still worth playing.
 *
 * Narration that has fallen behind is not narration, it is a second voice
 * talking over the next line. A clip whose moment has passed is dropped rather
 * than played late — and dropping is what removes the need for a fixed delay
 * before narration may start at all. The old code waited ten seconds so that
 * the first fetches would always be ready in time; with late clips discarded,
 * the wait can simply be nothing, and how quickly narration comes in becomes a
 * question of how fast the machine is rather than of a constant someone picked.
 */
export function shouldPlay(cueStart: number, readyAt: number): boolean {
  return readyAt <= cueStart
}

/**
 * How fast to read so the clip fits, given what it measures at natural pace.
 *
 * Never below the default — a slot with room to spare is not a reason to drawl
 * — and never above the maximum, past which the voice stops being followable
 * whatever the timing says.
 */
export function speedFor(naturalDuration: number, slot: number): number {
  if (!(naturalDuration > 0) || !(slot > 0)) return DEFAULT_SPEED
  const needed = naturalDuration / slot
  return Math.min(MAX_SPEED, Math.max(DEFAULT_SPEED, needed))
}

/**
 * When a clip should start, given where the video is now.
 *
 * Cue times are video time; the audio clock is its own. This converts between
 * them at the moment of scheduling, and never returns a moment already gone.
 */
export function startTimeFor(cueStart: number, videoTime: number, audioNow: number): number {
  return audioNow + Math.max(0, cueStart - videoTime)
}

/**
 * How far a clip may start after its cue before it is dropped instead.
 *
 * Not zero: clips run over their slot all the time, and silencing a line
 * because it starts a quarter-second late would silence most of them.
 */
export const MAX_LATENESS_SECONDS = 0.75

/** Seconds a scheduled clip starts after the cue it belongs to. */
export function latenessOf(scheduledStart: number, cueStart: number): number {
  return Math.max(0, scheduledStart - cueStart)
}

/**
 * Whether a clip has been pushed too far past its cue to be worth playing.
 *
 * shouldPlay asks whether a clip was *fetched* in time. This asks the question
 * that was missing: whether, after being queued behind a clip that overran, it
 * still lands anywhere near the line it is reading. A cue whose audio does not
 * fit even at 3x pushes the clip after it, which pushes the one after that, and
 * nothing ever compared the scheduled moment back against the cue — so the
 * voice slid further behind the picture for the rest of the video.
 *
 * Dropping one line lets the queue catch up with the video. It is the same
 * trade shouldPlay already makes: a missing line beats a voice reading the
 * previous sentence over the current one.
 */
export function tooLateToPlay(scheduledStart: number, cueStart: number): boolean {
  return latenessOf(scheduledStart, cueStart) > MAX_LATENESS_SECONDS
}

/** Breath between clips, when there is room for one. */
export const GAP_BETWEEN_CLIPS = 0.25

/**
 * When a clip should actually start, given when it is due and when the clip
 * before it ends.
 *
 * The gap is a courtesy, and the first thing to give up. Applying it
 * unconditionally is what turned a single overrun into permanent drift: a cue
 * whose audio does not fit even at 3x already ends past the next cue's moment,
 * and adding a quarter-second on top pushed the next clip further out, and the
 * one after that further still. Measured over fourteen consecutive cues of a
 * real video, that compounded from 0.20s late to 1.68s and was still climbing.
 *
 * Dropping the gap once the voice is behind absorbs the overrun instead of
 * passing it on: the same fourteen cues then peak at 0.12s late, with none
 * skipped.
 */
export function scheduleAt(due: number, previousEnd: number): number {
  const behind = previousEnd + GAP_BETWEEN_CLIPS > due
  return Math.max(due, previousEnd + (behind ? 0 : GAP_BETWEEN_CLIPS))
}
