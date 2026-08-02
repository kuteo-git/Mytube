import { useSyncExternalStore } from 'react'

/**
 * Videos the viewer has taken off the home page, and why.
 *
 * A store rather than a function that reads storage, because more than one
 * place shows videos. It used to be read during render and hidden only from the
 * feed, by editing that query's cache — so pressing "not interested" on a card
 * in "Popular with you" removed it from a list it was not in and left it
 * sitting where it was. Nothing told the other sections anything had happened.
 *
 * The reason is kept even though both reasons hide. "I have seen this" and "stop
 * showing me this" are different statements, and flattening them now would make
 * them impossible to tell apart later — when, for instance, the recommender
 * wants to learn from one and not the other.
 */
export type HiddenReason = 'not-interested' | 'watched'

const KEY = 'yt-hidden-videos-v2'

/** The previous key held a bare array with no reasons. */
const LEGACY_KEY = 'yt-hidden-videos'

type Hidden = Record<string, HiddenReason>

let entries: Hidden = load()
const listeners = new Set<() => void>()

/** A stable snapshot: React compares by identity, so this changes only on write. */
let snapshot: ReadonlySet<string> = new Set(Object.keys(entries))

function load(): Hidden {
  try {
    const raw = localStorage.getItem(KEY)
    if (raw) return JSON.parse(raw) as Hidden

    // Carry the old list across rather than losing what people already hid.
    const legacy = localStorage.getItem(LEGACY_KEY)
    if (legacy) {
      const ids = JSON.parse(legacy) as string[]
      return Object.fromEntries(ids.map((id) => [id, 'not-interested' as const]))
    }
  } catch {
    /* unreadable or unparseable; start clean */
  }
  return {}
}

function commit() {
  snapshot = new Set(Object.keys(entries))
  try {
    localStorage.setItem(KEY, JSON.stringify(entries))
  } catch {
    /* quota or private mode */
  }
  for (const listener of listeners) listener()
}

export function hideVideo(videoId: string, reason: HiddenReason) {
  if (!videoId || entries[videoId] === reason) return
  entries = { ...entries, [videoId]: reason }
  commit()
}

export function unhideVideo(videoId: string) {
  if (!(videoId in entries)) return
  const { [videoId]: _removed, ...rest } = entries
  entries = rest
  commit()
}

export function hiddenReason(videoId: string): HiddenReason | undefined {
  return entries[videoId]
}

/** Every hidden id, re-read whenever one is added or removed. */
export function useHiddenVideos(): ReadonlySet<string> {
  return useSyncExternalStore(subscribe, () => snapshot, () => snapshot)
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/** For tests, which must not inherit one another's hidden videos. */
export function resetHidden() {
  entries = {}
  commit()
}
