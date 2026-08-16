import { useCallback, useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { httpProfileRepository } from '../infrastructure/profileRepository'
import {
  currentProfileID,
  onProfileChange,
  setCurrentProfileID,
} from '../infrastructure/current-profile'
import type { Profile } from '../domain/profile'

/** The household's list. Small, and changes about once a year. */
export function useProfiles() {
  return useQuery({
    queryKey: ['profiles'],
    queryFn: () => httpProfileRepository.list(),
    staleTime: 5 * 60 * 1000,
  })
}

/**
 * Who this browser is, and how to become somebody else.
 *
 * Switching drops the entire query cache rather than any particular key.
 * Practically everything the app has fetched is answered per user — the feed,
 * subscriptions, history, reactions, up-next — and a switch that left any of it
 * behind would show one person's shelf under another person's name, which is
 * the one failure this whole feature exists to prevent.
 */
export function useCurrentProfile() {
  const [id, setID] = useState(currentProfileID)
  const queryClient = useQueryClient()

  useEffect(() => onProfileChange(setID), [])

  const choose = useCallback(
    (profile: Profile) => {
      setCurrentProfileID(profile.id)
      queryClient.clear()
    },
    [queryClient],
  )

  return { id, chosen: id !== '', choose }
}
