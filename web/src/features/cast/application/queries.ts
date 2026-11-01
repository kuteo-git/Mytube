import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import type { CastDevice, CastSession } from '../domain/cast'
import { castRepository } from '../infrastructure/cast-repository'
import { useCast } from './cast-context'

/**
 * How often a session is asked about.
 *
 * Every second while the remote is open, so a pause pressed on another phone
 * shows here within two; every five otherwise, which is only for the mini
 * bar's "something is playing on the R1". Neither runs in a hidden tab: React
 * Query stops a refetch interval while the document is hidden.
 */
export const REMOTE_POLL_MS = 1000
export const IDLE_POLL_MS = 5000

const devicesKey = ['cast', 'devices'] as const
const sessionsKey = ['cast', 'sessions'] as const
const sessionKey = (id: string) => ['cast', 'session', id] as const

/**
 * The speakers answering now.
 *
 * Asked once when a player appears and kept for a minute, because whether to
 * draw the cast button at all depends on it, and asked again whenever the
 * picker opens so the list in it is fresh.
 */
export function useCastDevices(enabled = true) {
  return useQuery<CastDevice[]>({
    queryKey: devicesKey,
    queryFn: castRepository.devices,
    enabled,
    staleTime: 60_000,
    retry: false,
  })
}

/** Sessions running anywhere in the house, for the mini bar. */
export function useCastSessions() {
  return useQuery<CastSession[]>({
    queryKey: sessionsKey,
    queryFn: castRepository.sessions,
    refetchInterval: IDLE_POLL_MS,
    retry: false,
  })
}

/** The session the remote shows, ended ones included, so it can say why. */
export function useCastSession(id: string | null) {
  return useQuery<CastSession>({
    queryKey: sessionKey(id ?? ''),
    queryFn: () => castRepository.session(id!),
    enabled: Boolean(id),
    refetchInterval: REMOTE_POLL_MS,
    retry: false,
  })
}

function useRemember() {
  const queryClient = useQueryClient()
  return (session: CastSession) => {
    queryClient.setQueryData(sessionKey(session.id), session)
    void queryClient.invalidateQueries({ queryKey: sessionsKey })
  }
}

export interface StartCastInput {
  device: CastDevice
  videoIds: string[]
  startPositionSeconds: number
}

/** Starts playing on a speaker and opens the remote on it. */
export function useStartCast() {
  const remember = useRemember()
  const { openRemote } = useCast()
  return useMutation({
    mutationFn: (input: StartCastInput) =>
      castRepository.start({
        deviceId: input.device.id,
        videoIds: input.videoIds,
        startIndex: 0,
        startPositionSeconds: Math.max(0, Math.round(input.startPositionSeconds)),
      }),
    onSuccess: (session) => {
      remember(session)
      openRemote(session.id)
    },
  })
}

/** Every command the remote sends, each answering with the session. */
export function useCastControls(id: string) {
  const onSuccess = useRemember()
  return {
    pause: useMutation({ mutationFn: () => castRepository.pause(id), onSuccess }),
    resume: useMutation({ mutationFn: () => castRepository.resume(id), onSuccess }),
    next: useMutation({ mutationFn: () => castRepository.next(id), onSuccess }),
    stop: useMutation({ mutationFn: () => castRepository.stop(id), onSuccess }),
    seek: useMutation({ mutationFn: (seconds: number) => castRepository.seek(id, seconds), onSuccess }),
    volume: useMutation({ mutationFn: (percent: number) => castRepository.volume(id, percent), onSuccess }),
    jump: useMutation({ mutationFn: (index: number) => castRepository.jump(id, index), onSuccess }),
    replaceQueue: useMutation({
      mutationFn: (edit: { version: number; videoIds: string[]; currentIndex: number }) =>
        castRepository.replaceQueue(id, edit.version, edit.videoIds, edit.currentIndex),
      onSuccess,
    }),
  }
}

/**
 * Stops the speaker and carries on here, from where it was.
 *
 * The player picks it up in place when it already holds that video; otherwise
 * the watch page opens it, and resumes from the position the cast service has
 * just written to history.
 */
export function useHandoff(id: string) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { closeRemote, resumeLocally } = useCast()
  return useMutation({
    mutationFn: () => castRepository.handoff(id),
    onSuccess: async ({ videoId, positionSeconds }) => {
      closeRemote()
      void queryClient.invalidateQueries({ queryKey: sessionsKey })
      if (resumeLocally(videoId, positionSeconds)) return
      await queryClient.invalidateQueries({ queryKey: ['video'] })
      navigate(`/watch/${encodeURIComponent(videoId)}`)
    },
  })
}
