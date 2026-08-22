import { useEffect } from 'react'

import { useCurrentProfile, useProfiles } from '../application/use-profile'
import { setCurrentProfileID } from '../infrastructure/current-profile'
import { ProfilePicker } from './ProfilePicker'

/**
 * Asks who is watching, but only when there is something to ask.
 *
 * A household with one profile has no question to answer, and putting a screen
 * in front of it would be a gate guarding an empty room — the gateway already
 * falls back to the configured user when no header arrives, so a browser that
 * has never chosen behaves exactly as every browser did before profiles
 * existed. The picker appears the moment a second person is added, which is the
 * moment the answer starts to matter.
 *
 * The consequence worth stating: the person who adds the second profile is
 * choosing for a household of browsers that have each been silently answering
 * "the default user" until then. Their history is not moved anywhere — it stays
 * on the id it was always on, which is the one the picker offers first.
 */
export function ProfileGate({ children }: { children: React.ReactNode }) {
  const { data: profiles, isLoading } = useProfiles()
  const { id, chosen } = useCurrentProfile()

  // A choice that no longer names anybody is not a choice.
  //
  // This asked only whether *an* id had been stored, never whether it still
  // existed — so a device holding a deleted profile's id went on putting it in
  // `X-User-Id` for ever, and the gateway went on answering for a ghost with
  // its own feed and its own history, all keyed to rows that are gone. Nothing
  // on screen said so.
  //
  // The server cannot repair it: `localStorage` is per device, and the deletion
  // happens on somebody else's. So the device repairs itself here, the next
  // time it loads, against the list it has just fetched.
  const missing = Boolean(chosen && profiles && !profiles.some((p) => p.id === id))
  useEffect(() => {
    if (missing) setCurrentProfileID('')
  }, [missing])

  // While the list is loading, show the app rather than a spinner. Being
  // momentarily wrong about which profile is asking costs a re-fetch; a blank
  // screen on every cold load costs the whole first impression.
  if (isLoading || (chosen && !missing) || !profiles || profiles.length < 2) {
    return <>{children}</>
  }
  return <ProfilePicker />
}
