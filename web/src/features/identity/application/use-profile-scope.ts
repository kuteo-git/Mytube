import { useEffect, useState } from 'react'

import {
  currentProfileID,
  onProfileChange,
} from '../infrastructure/current-profile'

/**
 * The current profile, for use inside a query key.
 *
 * Everything the gateway answers per user — the feed, up-next, a video's own
 * watched state, history, subscriptions, saved — is cached by React Query under
 * a key that said nothing about who asked. Switching profile cleared the whole
 * cache to compensate, and that worked, but it made the separation of two
 * people's data depend on a side effect firing at the right moment: get that
 * wrong once and somebody sees another person's shelf under their own name,
 * with nothing on screen to say so.
 *
 * In the key it is structural instead. Two people's answers cannot collide
 * because they are not the same entry, a component that never re-renders on the
 * switch still refetches because its key changed, and `clear()` becomes belt
 * and braces rather than the only thing holding this together.
 *
 * Re-renders on a switch, which is what makes the key change at all.
 */
export function useProfileScope(): string {
  const [id, setID] = useState(currentProfileID)
  useEffect(() => onProfileChange(setID), [])
  return id
}
