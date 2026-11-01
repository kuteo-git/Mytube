import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from 'react'

/**
 * Which session the remote is showing, and how to carry on here after a
 * handoff.
 *
 * Above the router, like the player, because both outlive a page: a remote
 * opened on Watch stays open on Home, and the mini bar is on every page.
 */
interface CastContextValue {
  remoteSessionId: string | null
  openRemote: (sessionId: string) => void
  closeRemote: () => void
  /**
   * The player says how it would pick a video back up at a position, and
   * whether it could (it can only for the video it already holds).
   */
  setLocalResume: (resume: LocalResume | null) => void
  resumeLocally: (videoId: string, positionSeconds: number) => boolean
}

export type LocalResume = (videoId: string, positionSeconds: number) => boolean

const CastContext = createContext<CastContextValue | null>(null)

export function useCast(): CastContextValue {
  const ctx = useContext(CastContext)
  if (!ctx) throw new Error('useCast must be used within CastProvider')
  return ctx
}

export function CastProvider({ children }: { children: ReactNode }) {
  const [remoteSessionId, setRemoteSessionId] = useState<string | null>(null)
  const localResume = useRef<LocalResume | null>(null)

  const setLocalResume = useCallback((resume: LocalResume | null) => {
    localResume.current = resume
  }, [])
  const resumeLocally = useCallback(
    (videoId: string, positionSeconds: number) => localResume.current?.(videoId, positionSeconds) ?? false,
    [],
  )
  const closeRemote = useCallback(() => setRemoteSessionId(null), [])

  const value = useMemo(
    () => ({ remoteSessionId, openRemote: setRemoteSessionId, closeRemote, setLocalResume, resumeLocally }),
    [remoteSessionId, closeRemote, setLocalResume, resumeLocally],
  )
  return <CastContext.Provider value={value}>{children}</CastContext.Provider>
}
