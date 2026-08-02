/**
 * The video that was playing when the tab was last closed.
 *
 * Kept so that opening the app again offers it back in the corner rather than
 * pretending nothing was happening. Closing a tab is rarely a decision to stop
 * watching; it is usually a decision to do something else first.
 *
 * Deliberately not the server's watch history. History says what was watched,
 * ever, across every device; this says what *this* browser was in the middle of
 * a moment ago. Resuming from history would offer back something finished on a
 * different machine last week.
 */

const KEY = 'yt-last-watched'

/** Past this fraction the video counts as finished, and is not offered back. */
const FINISHED_FRACTION = 0.95

/** After this long it is no longer "a moment ago". */
const STALE_AFTER_MS = 7 * 24 * 60 * 60 * 1000

export interface LastWatched {
  videoId: string
  positionSeconds: number
  /** Milliseconds since the epoch, for deciding whether this is still recent. */
  savedAt: number
}

export function rememberLastWatched(
  videoId: string,
  positionSeconds: number,
  durationSeconds: number,
  now = Date.now(),
): void {
  if (!videoId) return

  // A video watched to the end has nothing to resume. Offering it back would
  // mean a corner window that plays two seconds of credits and stops.
  if (durationSeconds > 0 && positionSeconds / durationSeconds >= FINISHED_FRACTION) {
    forgetLastWatched()
    return
  }

  write({ videoId, positionSeconds: Math.max(0, Math.floor(positionSeconds)), savedAt: now })
}

export function forgetLastWatched(): void {
  try {
    window.localStorage.removeItem(KEY)
  } catch {
    /* storage unavailable */
  }
}

/**
 * What to offer back, or null.
 *
 * Anything older than a week is dropped rather than returned. Coming back after
 * a fortnight to a corner window of whatever was on before the holiday is not a
 * courtesy; the intent it was recording has long since expired.
 */
export function readLastWatched(now = Date.now()): LastWatched | null {
  let raw: string | null = null
  try {
    raw = window.localStorage.getItem(KEY)
  } catch {
    return null
  }
  if (!raw) return null

  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    forgetLastWatched()
    return null
  }

  const entry = parsed as Partial<LastWatched>
  if (typeof entry?.videoId !== 'string' || !entry.videoId) {
    forgetLastWatched()
    return null
  }
  if (typeof entry.savedAt !== 'number' || now - entry.savedAt > STALE_AFTER_MS) {
    forgetLastWatched()
    return null
  }

  return {
    videoId: entry.videoId,
    positionSeconds: typeof entry.positionSeconds === 'number' ? entry.positionSeconds : 0,
    savedAt: entry.savedAt,
  }
}

function write(entry: LastWatched): void {
  try {
    window.localStorage.setItem(KEY, JSON.stringify(entry))
  } catch {
    /* quota or private mode; resuming is a courtesy, not a requirement */
  }
}
