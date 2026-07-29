import { useEffect, useState } from 'react'

const STORAGE_KEY = 'watch-trail'
/**
 * Long enough that a sitting fits in it, short enough that yesterday's viewing
 * does not keep suppressing suggestions.
 */
const MAX_TRAIL = 50

/**
 * The videos watched so far in this sitting.
 *
 * Recommendations are symmetric: the best suggestion after B is very often A,
 * the video that was just playing. Following the rail blindly therefore
 * bounces between two videos forever. The trail is what breaks that — "next"
 * and the rail both skip anything already played here.
 *
 * sessionStorage, not localStorage: it is a property of the current sitting,
 * and a viewer coming back tomorrow should be offered these videos again.
 */
function readTrail(): string[] {
  try {
    const raw = window.sessionStorage.getItem(STORAGE_KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === 'string') : []
  } catch {
    // A corrupted entry must not take the watch page down with it.
    return []
  }
}

function appendTrail(videoId: string): string[] {
  const next = [videoId, ...readTrail().filter((id) => id !== videoId)].slice(0, MAX_TRAIL)
  try {
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  } catch {
    // Private mode and full quotas both land here; the in-memory set still works.
  }
  return next
}

const ADVANCE_KEY = 'watch-advanced-to'

/**
 * How long an advance marker stays good.
 *
 * Long enough to survive the navigation that follows it — a few hundred
 * milliseconds in practice — and far too short to still be there if someone
 * wanders back to the same video later in the sitting, which must resume
 * normally.
 */
const ADVANCE_WINDOW_MS = 15_000

/**
 * Records that the next video is being arrived at by advancing, not by being
 * chosen.
 *
 * In sessionStorage rather than React state or router state, and the choice is
 * load-bearing. React state has to survive a navigation, a suspense fallback
 * and a query refetch to still be there when the player mounts, and the first
 * two attempts at this failed somewhere in that sequence in a way that could
 * not be reproduced by reading the code. Router state survives all of it but
 * lives in history, so a reload or a Back replays the fresh start and throws
 * away a position the viewer had genuinely built up. A timestamped marker in
 * sessionStorage has neither problem: nothing can lose it, and it expires on
 * its own.
 */
export function markAdvancedTo(videoId: string): void {
  try {
    window.sessionStorage.setItem(
      ADVANCE_KEY,
      JSON.stringify({ id: videoId, at: Date.now() }),
    )
  } catch {
    // Private mode and full quotas both land here. The cost is a video that
    // resumes when it should not have, which is not worth failing over.
  }
}

/**
 * Whether this video was arrived at by advancing.
 *
 * A pure read — no clearing, no ordering requirement, nothing to run in the
 * right effect at the right time. It answers the same way on every render for
 * as long as it is true, which is what stops the player being told to seek
 * somewhere new halfway through a video.
 */
export function arrivedByAdvancing(videoId: string | undefined): boolean {
  if (!videoId) return false
  try {
    const raw = window.sessionStorage.getItem(ADVANCE_KEY)
    if (!raw) return false
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return false
    const { id, at } = parsed as { id?: unknown; at?: unknown }
    if (id !== videoId || typeof at !== 'number') return false
    return Date.now() - at < ADVANCE_WINDOW_MS
  } catch {
    return false
  }
}

/**
 * Records the video being watched and returns everything played this sitting,
 * the current video included — so a caller can filter with it directly.
 */
export function useWatchTrail(videoId: string | undefined): ReadonlySet<string> {
  const [trail, setTrail] = useState<ReadonlySet<string>>(() => new Set(readTrail()))

  useEffect(() => {
    if (!videoId) return
    setTrail(new Set(appendTrail(videoId)))
  }, [videoId])

  return trail
}
