/**
 * How long the translation pass still has to run.
 *
 * The count alone does not answer the question people actually have, which is
 * whether to wait or to go and do something else. A pass over an hour-long
 * video is minutes of work, and minutes with no end in sight read as broken.
 */

/**
 * Cues that must be translated before a rate means anything.
 *
 * More than the opening batch of three, deliberately. That batch is small by
 * design and may carry the model load with it — measured at 31.5s cold against
 * 3s warm — so a rate taken from it alone said nine minutes where the truth was
 * three. An estimate that appears late is better than one that appears wrong
 * and then corrects itself downwards by a factor of three.
 */
const MIN_SAMPLE = 8

export function estimateEtaSeconds({
  done,
  total,
  baseline,
  elapsedMs,
}: {
  done: number
  total: number
  /** Cues already translated when the pass began — seeded from disk. */
  baseline: number
  elapsedMs: number
}): number | null {
  if (total > 0 && done >= total) return 0

  // Only cues this pass actually translated tell you how fast it is going.
  // Counting the ones that came off disk instantly would put the estimate at
  // almost nothing while minutes of work remained.
  const translated = done - baseline
  if (translated < MIN_SAMPLE || elapsedMs <= 0) return null

  const perSecond = translated / (elapsedMs / 1000)
  if (perSecond <= 0) return null
  return Math.round((total - done) / perSecond)
}

export function formatDuration(seconds: number): string {
  if (seconds < 5) return 'vài giây'
  if (seconds < 60) return `khoảng ${Math.round(seconds / 5) * 5} giây`
  const minutes = seconds / 60
  if (minutes < 10) return `khoảng ${Math.round(minutes)} phút`
  // Past ten minutes nobody acts on the difference between 47 and 52, and a
  // figure that precise invites watching it tick.
  return `khoảng ${Math.round(minutes / 5) * 5} phút`
}
