/**
 * Which profile this browser is.
 *
 * `localStorage`, so it survives a reload and does not survive a different
 * machine — which is the behaviour wanted: the profile answers "who is
 * watching", and that travels with the browser rather than with the hardware.
 * The same laptop can be somebody else tomorrow, and the television can be
 * whoever picked up the remote.
 *
 * Deliberately not React state and not a context value. The HTTP layer needs to
 * read it on every request, including from code that is nowhere near a
 * component, and threading it through would put identity into the signature of
 * every repository method in the app.
 */

import { NO_PROFILE } from '../domain/profile'

const KEY = 'yt-profile-id-v1'

/** Notified when the profile changes, so open queries can be dropped. */
const listeners = new Set<(id: string) => void>()

export function currentProfileID(): string {
  try {
    return window.localStorage.getItem(KEY) ?? NO_PROFILE
  } catch {
    // Private browsing, or storage disabled. The gateway's own fallback covers
    // this: no header means the default user, which is what the household had
    // before profiles existed.
    return NO_PROFILE
  }
}

export function setCurrentProfileID(id: string): void {
  try {
    if (id === NO_PROFILE) window.localStorage.removeItem(KEY)
    else window.localStorage.setItem(KEY, id)
  } catch {
    // Nothing to do. The session continues as the default user rather than
    // failing, which is the same trade every other preference here makes.
  }
  for (const listener of listeners) listener(id)
}

export function onProfileChange(listener: (id: string) => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}
