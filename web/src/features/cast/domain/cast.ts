/**
 * Playing on the household's speakers: the R1 and the Google Nest speakers.
 *
 * The session lives on the server (the cast service, through the gateway), so
 * every browser and phone in the house sees the same one and closing this tab
 * changes nothing on the speaker. This app only ever asks and shows.
 */

export type DeviceKind = 'r1' | 'google'

export interface CastDevice {
  id: string
  name: string
  kind: DeviceKind
  model: string
  /** The session playing on it now, if any. */
  activeSessionId?: string
}

export type SessionState = 'loading' | 'playing' | 'paused' | 'ended'

export type EndedReason =
  | 'queue_finished'
  | 'stopped'
  | 'replaced'
  | 'taken_over'
  | 'stopped_on_device'
  | 'device_offline'
  | 'handoff'
  | 'failed'

export interface CastItem {
  videoId: string
  title: string
  channel: string
  thumbnailUrl: string
  durationSeconds: number
}

export interface CastSession {
  id: string
  device: { id: string; name: string; kind: DeviceKind }
  state: SessionState
  /** Null while the session is running. */
  endedReason: EndedReason | null
  queue: CastItem[]
  currentIndex: number
  positionSeconds: number
  durationSeconds: number
  volume: number
  /** Changes with the queue, never with the position. A queue edit names it. */
  version: number
  startedBy: string
}

export function currentItem(session: CastSession): CastItem | undefined {
  return session.queue[session.currentIndex]
}

/**
 * Whether an end needs saying. The viewer pressing stop, the queue running
 * out and handing back to the phone are their own acts or plain to see; the
 * speaker being taken, stopped from the speaker, lost or failing are not, and
 * a remote that silently closed on them would look broken.
 */
export function endNeedsSaying(reason: EndedReason | null): boolean {
  return (
    reason === 'taken_over' ||
    reason === 'stopped_on_device' ||
    reason === 'device_offline' ||
    reason === 'failed' ||
    reason === 'replaced'
  )
}

/** The queue with one item moved, for a drag or a move-up press. */
export function moveItem<T>(items: readonly T[], from: number, to: number): T[] {
  const out = [...items]
  if (from < 0 || from >= out.length || to < 0 || to >= out.length) return out
  const [moved] = out.splice(from, 1)
  out.splice(to, 0, moved)
  return out
}

/**
 * Where the current item lands once the queue has been edited, so the song
 * playing keeps playing. The server checks this too; sending the right index
 * spares it a reload.
 */
export function indexAfterEdit(before: CastSession, after: readonly CastItem[]): number {
  const playing = currentItem(before)
  if (!playing) return 0
  const found = after.findIndex((it) => it.videoId === playing.videoId)
  if (found >= 0) return found
  return Math.min(before.currentIndex, Math.max(after.length - 1, 0))
}
