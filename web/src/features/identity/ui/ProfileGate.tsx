import { useCurrentProfile, useProfiles } from '../application/use-profile'
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
  const { chosen } = useCurrentProfile()

  // While the list is loading, show the app rather than a spinner. Being
  // momentarily wrong about which profile is asking costs a re-fetch; a blank
  // screen on every cold load costs the whole first impression.
  if (isLoading || chosen || !profiles || profiles.length < 2) {
    return <>{children}</>
  }
  return <ProfilePicker />
}
