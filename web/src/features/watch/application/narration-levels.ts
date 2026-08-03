/**
 * How loud the voice and the video are, given what the viewer set.
 *
 * The two used to be chained rather than independent. The player ducked the
 * video to a fifth, and narration then took its gain from `video.volume` — the
 * already-ducked figure — so the constant that looked like "2.5× louder" was
 * really 0.5× of master, and moving the video level dragged the voice with it.
 * Nobody could set one without changing the other.
 *
 * Both are now plain fractions of master, computed here so the relationship can
 * be tested without an audio clock.
 */
export interface NarrationLevels {
  /** Gain for the narration bus. */
  narration: number
  /** Volume for the video element. */
  video: number
}

export function levelsFor({
  master,
  muted,
  narrating,
  narrationLevel,
  duckLevel,
}: {
  /** The player's own volume control, 0–1. */
  master: number
  muted: boolean
  /** Whether narration is speaking — ducking applies only then. */
  narrating: boolean
  /** Voice level as a fraction of master. Above 1 is allowed: TTS is quieter
   *  than film audio, and a limiter sits after this. */
  narrationLevel: number
  /** What the video drops to while the voice is speaking. */
  duckLevel: number
}): NarrationLevels {
  if (muted) return { narration: 0, video: 0 }
  if (!narrating) return { narration: 0, video: master }
  return {
    narration: master * narrationLevel,
    video: master * duckLevel,
  }
}
