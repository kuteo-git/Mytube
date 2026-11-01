import { apiJSON, HttpError } from '@/shared/api/http'
import type { CastDevice, CastItem, CastSession, EndedReason } from '../domain/cast'

/** The gateway's shapes, snake_case as the gateway writes them. */
interface DeviceDTO {
  id: string
  name: string
  kind: 'r1' | 'google'
  model?: string
  online: boolean
  active_session_id?: string
}

interface ItemDTO {
  video_id: string
  title: string
  channel: string
  thumbnail_url: string
  duration_s: number
}

interface SessionDTO {
  id: string
  device: DeviceDTO
  state: CastSession['state']
  ended_reason: EndedReason | null
  queue: ItemDTO[] | null
  current_index: number
  position_s: number
  duration_s: number
  volume: number
  version: number
  started_by: string
}

/**
 * Why the gateway refused, as the code it sends. A screen decides what to say
 * from it; this layer never translates (§4b).
 */
export type CastErrorCode =
  | 'not_found'
  | 'stale_version'
  | 'session_ended'
  | 'live'
  | 'device_offline'
  | 'unknown'

export class CastError extends Error {
  constructor(
    readonly code: CastErrorCode,
    readonly status: number,
  ) {
    super(code)
    this.name = 'CastError'
  }
}

function toDevice(d: DeviceDTO): CastDevice {
  return { id: d.id, name: d.name, kind: d.kind, model: d.model ?? '', activeSessionId: d.active_session_id }
}

function toItem(it: ItemDTO): CastItem {
  return {
    videoId: it.video_id,
    title: it.title,
    channel: it.channel,
    thumbnailUrl: it.thumbnail_url,
    durationSeconds: it.duration_s,
  }
}

export function toSession(s: SessionDTO): CastSession {
  return {
    id: s.id,
    device: { id: s.device.id, name: s.device.name, kind: s.device.kind },
    state: s.state,
    endedReason: s.ended_reason ?? null,
    queue: (s.queue ?? []).map(toItem),
    currentIndex: s.current_index,
    positionSeconds: s.position_s,
    durationSeconds: s.duration_s,
    volume: s.volume,
    version: s.version,
    startedBy: s.started_by,
  }
}

const KNOWN: readonly CastErrorCode[] = ['not_found', 'stale_version', 'session_ended', 'live', 'device_offline']

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  try {
    return await apiJSON<T>(path, init)
  } catch (err) {
    if (!(err instanceof HttpError)) throw err
    let code: CastErrorCode = 'unknown'
    try {
      const parsed = JSON.parse(err.message) as { code?: string }
      if (parsed.code && (KNOWN as readonly string[]).includes(parsed.code)) code = parsed.code as CastErrorCode
    } catch {
      // Not JSON: a proxy or a crash in between. Unknown is the honest answer.
    }
    throw new CastError(code, err.status)
  }
}

const post = (body?: unknown): RequestInit => ({
  method: 'POST',
  body: body === undefined ? undefined : JSON.stringify(body),
})

const session = (id: string, action: string, body?: unknown) =>
  call<SessionDTO>(`/api/cast/sessions/${encodeURIComponent(id)}/${action}`, post(body)).then(toSession)

export interface StartCast {
  deviceId: string
  videoIds: string[]
  startIndex: number
  startPositionSeconds: number
}

export const castRepository = {
  devices: () => call<DeviceDTO[]>('/api/cast/devices').then((list) => list.map(toDevice)),
  sessions: () => call<SessionDTO[]>('/api/cast/sessions').then((list) => list.map(toSession)),
  session: (id: string) => call<SessionDTO>(`/api/cast/sessions/${encodeURIComponent(id)}`).then(toSession),
  start: (req: StartCast) =>
    call<SessionDTO>(
      '/api/cast/sessions',
      post({
        device_id: req.deviceId,
        items: req.videoIds.map((video_id) => ({ video_id })),
        start_index: req.startIndex,
        start_position_s: req.startPositionSeconds,
      }),
    ).then(toSession),
  pause: (id: string) => session(id, 'pause'),
  resume: (id: string) => session(id, 'resume'),
  next: (id: string) => session(id, 'next'),
  stop: (id: string) => session(id, 'stop'),
  seek: (id: string, positionSeconds: number) => session(id, 'seek', { position_s: positionSeconds }),
  volume: (id: string, percent: number) => session(id, 'volume', { percent }),
  jump: (id: string, index: number) => session(id, 'jump', { index }),
  replaceQueue: (id: string, version: number, videoIds: string[], currentIndex: number) =>
    call<SessionDTO>(`/api/cast/sessions/${encodeURIComponent(id)}/queue`, {
      method: 'PUT',
      body: JSON.stringify({ version, items: videoIds.map((video_id) => ({ video_id })), current_index: currentIndex }),
    }).then(toSession),
  handoff: (id: string) =>
    call<{ video_id: string; position_s: number }>(
      `/api/cast/sessions/${encodeURIComponent(id)}/handoff`,
      post(),
    ).then((r) => ({ videoId: r.video_id, positionSeconds: r.position_s })),
}
