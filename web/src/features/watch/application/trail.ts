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
