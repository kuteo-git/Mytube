import { apiFetch } from '@/shared/api/http'
import type {
  Channel,
  Topic,
  Comment,
  Playlist,
  ReactionState,
  StorageUsage,
  Video,
} from '../domain/video'

/**
 * Repository port. The application layer depends on this interface only, so the
 * transport can change without touching a single hook or component.
 */
export interface CatalogRepository {
  listFeed(topic: string, pageToken?: string): Promise<Feed>
  getVideo(id: string): Promise<Video>
  /**
   * The video, creating its catalog row first if it has none.
   *
   * A queue is a channel's uploads read live from YouTube, so most of what it
   * lists has never been ingested. Opening one from a card writes the row
   * before navigating, but arriving by "next", by a queue link or by a pasted
   * URL does not — and those must not land on "Video not found".
   */
  getVideoEnsuring(id: string): Promise<Video>
  listUpNext(currentVideoId: string, channelFilter?: string, pageToken?: string, pageSize?: number): Promise<Feed>
  listComments(videoId: string, pageToken?: string, pageSize?: number): Promise<CommentPage>
  listTopics(): Promise<Topic[]>
  refreshTopics(): Promise<ScanStatus>
  getScanStatus(): Promise<ScanStatus>
  search(query: string, pageToken?: string): Promise<Feed>
  suggest(query: string): Promise<Suggestion[]>
  discover(query: string, limit: number): Promise<ExternalVideo[]>
  /**
   * The channel a pasted address names, or null when it names none.
   *
   * Answered by the gateway rather than parsed here, because which channel an
   * address names is one rule and lives in one place — the same reason the
   * search page's own link check is deliberately cruder than the real parser.
   *
   * Null for an ordinary search term, which is most of what this is asked
   * about, and free in that case: the gateway answers without touching the
   * catalog or YouTube.
   */
  resolveChannel(query: string): Promise<string | null>
  ensureExternal(sourceUrl: string): Promise<string>
  getStorage(): Promise<StorageUsage>
  /** Videos the viewer has pinned as worth keeping. */
  listPinned(pageToken?: string): Promise<Feed>
  setPinned(videoId: string, pinned: boolean): Promise<void>
  /**
   * The member's playlists, as their YouTube account has them. Read-only: this
   * is a mirror refreshed on every account scan, so there is nothing to write.
   */
  listPlaylists(): Promise<Playlist[]>
  getPlaylist(id: string, pageToken?: string): Promise<{ playlist: Playlist; videos: Video[]; nextPageToken?: string }>
  /** The member's Watch later, from the same mirror. Read-only for the same reason. */
  listWatchLater(pageToken?: string): Promise<Feed>
  /** Every video the viewer has watched, most recent first. */
  listHistory(pageToken?: string): Promise<Feed>

  /**
   * Lists every way a video can be played right now, best-effort: the local
   * file if it is on disk, otherwise an instantly playable upstream URL and a
   * full-resolution stream muxed on demand.
   *
   * `prefetch` means the viewer has only hovered, not pressed play: resolve and
   * cache the upstream URL so a later press is instant, but schedule no
   * download. The disk has a hard ceiling, and drifting a mouse across a feed
   * must not fill it.
   */
  getStream(videoId: string, prefetch?: boolean): Promise<StreamSources>
  listJobs(activeOnly: boolean, options?: JobListOptions): Promise<IngestJob[]>
  /**
   * Stops the transfer for a video, if one is running.
   *
   * Sent on leaving the watch page. Pressing play schedules a copy so the video
   * is on disk next time, but a copy nobody is waiting for is a request to
   * YouTube nobody is waiting for either — and too many of those get the
   * address blocked.
   */
  cancelDownload(videoId: string): Promise<void>
  cancelJob(jobId: string): Promise<void>
  /** Hides a finished job. Running ones are cancelled, not hidden. */
  dismissJob(jobId: string): Promise<void>
  /** Queues the same URL again and returns the new job. */
  retryJob(jobId: string): Promise<IngestJob>
  listScans(limit: number, offset: number): Promise<ScanPage>
  /** Deletes every scan row. */
  clearScans(): Promise<void>
  /** Dismisses all jobs with a given state, returning the count. */
  dismissJobs(state: string): Promise<number>

  recordProgress(videoId: string, positionSeconds: number, watchedFraction: number): Promise<void>
  /** Hides a video from the feed and tells the recommender not to offer it. */
  recordNotInterested(videoId: string): Promise<void>
  setReaction(videoId: string, reaction: ReactionState): Promise<number>
  addComment(videoId: string, text: string, parentCommentId?: string): Promise<Comment>
	/** Fetches YouTube comments and imports them into the catalog. No-op when
	 *  comments already exist. */
	fetchComments(videoId: string): Promise<FetchCommentsResult>

  getChannel(channelId: string): Promise<ChannelPage>
  listChannelVideos(channelId: string, pageToken?: string): Promise<ChannelUploads>
  setSubscription(channelId: string, subscribed: boolean): Promise<void>
  listSubscriptions(): Promise<Channel[]>

  /** Videos this viewer has spent the most time on, in order. */
  listTopPlayed(limit?: number): Promise<Video[]>
  /** Widely-watched videos filtered to this viewer's taste. */
  listPopular(limit?: number): Promise<Video[]>
}


export interface FetchCommentsResult {
  imported: number
  skipped?: boolean
  /**
   * YouTube would not hand the comments over this time.
   *
   * A refusal rather than a failure, and temporary: the same video usually
   * answers on the next press. It arrives under a 200 for that reason — the
   * request did what it could, and nothing on the page depends on the answer.
   */
  unavailable?: boolean
}
export interface ChannelPage {
  channel: Channel
  videoCount: number
}

/**
 * A page of a channel's uploads, read live from YouTube rather than from the
 * local library — so browsing a channel is not capped at whatever a scan
 * happened to bring in.
 *
 * `sortOptions` is whatever orderings YouTube offers for that channel, not a
 * fixed list, so the control can never present an ordering that does nothing.
 * `nextPageToken` is empty when the channel has run out.
 */
export interface ChannelUploads {
  videos: ExternalVideo[]
  sortOptions: SortOption[]
  nextPageToken: string
}

export interface SortOption {
  label: string
  /** Opaque; selecting an ordering means passing this back as the page token. */
  token: string
}

export interface Feed {
  videos: Video[]
  nextPageToken?: string
}

export interface CommentPage {
  comments: Comment[]
  totalCount: number
  nextPageToken?: string
}

/** A video found upstream, which may or may not have a catalog row yet. */
export interface ExternalVideo {
  id: string
  title: string
  channelName: string
  durationSeconds: number
  viewCount: number
  thumbnailUrl: string
  sourceUrl: string
  /** ISO 8601, or absent when the source did not disclose an upload date. */
  publishedAt?: string
  inLibrary: boolean
}

export interface Suggestion {
  text: string
  kind: 'TITLE' | 'TOPIC' | 'CHANNEL'
  videoCount: number
}

/**
 * How the Activity page asks for jobs, and how nobody else does.
 *
 * hideDismissed is a request rather than a rule because the player reads this
 * same list to learn its download has landed. Filtering by default would let
 * somebody tidying this page hide a completed job from a player still waiting
 * on it.
 */
export interface JobListOptions {
  hideDismissed?: boolean
  limit?: number
}

export interface ScanPage {
  scans: ScanStatus[]
  total: number
}

export interface ScanStatus {
  startedAt: string
  durationMs: number
  sourcesScanned: number
  sourcesFailed: number
  videosSeen: number
  videosAdded: number
  errors: string[]
  /** True while a scan is in progress, across page refreshes and devices. */
  running: boolean
}

/** One way of playing a video. */
export interface StreamSource {
  url: string
  height?: number
  mimeType?: string
  /**
   * False only for the muxed-on-the-fly stream, which carries no index. The
   * player disables the seek bar rather than offer a control that cannot work.
   */
  seekable: boolean
  expiresAt?: string
}

/**
 * Every way a video can be played right now.
 *
 * The gateway lists rather than chooses, because choosing well needs to know
 * how far the viewer has watched and how much has buffered — and only the
 * player knows either.
 */
export interface StreamSources {
  /**
   * Progressive upstream: starts in milliseconds and seeks properly, but capped
   * at 360p by what YouTube still publishes muxed. Absent when the video offers
   * no progressive format.
   */
  instant?: StreamSource
  /**
   * The same two adaptive tracks as `remux`, described as a playlist so the
   * browser combines them itself — and, unlike the mux, **seekable**, because a
   * media playlist is an index.
   *
   * Offered alongside `remux` rather than instead of it: what can play it
   * differs by browser. Measured 2026-08-20 on iOS 18.7, same video minutes
   * apart — the mux reached `play()` and never produced a picture, this played
   * with a real duration and seeked twice. Chrome is the other way round.
   */
  hls?: StreamSource
  /** Full resolution, muxed live. Not seekable. Absent once `local` exists. */
  remux?: StreamSource
  /** The downloaded file. Present only once on disk, and best whenever it is. */
  local?: StreamSource
  /**
   * No copy is coming: the household has asked for streaming only.
   *
   * Carried in this answer rather than read from the settings separately, so
   * the player has one source for "what can play, and what is on its way".
   */
  cacheDisabled?: boolean
  /** When every source is unavailable — membership, age restriction, geo-block —
   *  this carries the yt-dlp error so the player can tell the viewer why. */
  streamError?: string
  /**
   * Set when this request found the catalogue claiming a file the disk does not
   * have, and corrected the row.
   *
   * The video was fetched before this was known, so the copy the page is
   * holding still says READY — and the poll that would refresh it stops once
   * the state looks settled. Without being told, the page would go on showing
   * the video as downloaded for as long as it stayed open.
   */
  repaired?: boolean
  /**
   * Upstream has refused this video for good: members-only, private, removed.
   *
   * Apart from `streamError`, which is a sentence about something that went
   * wrong and might not next time. This is an answer, and the player draws no
   * retry from it — the reason is a word from a closed set precisely so the UI
   * branches on it rather than reading English out of an error.
   */
  unavailable?: { reason: UnavailableReason }
}

export type UnavailableReason = 'members_only' | 'private' | 'removed' | 'unavailable'

export type JobState = 'QUEUED' | 'RUNNING' | 'SUCCEEDED' | 'FAILED' | 'CANCELLED'

export interface IngestJob {
  id: string
  sourceUrl: string
  videoId: string
  title: string
  state: JobState
  progress: number
  downloadedBytes: number
  totalBytes: number
  errorMessage?: string
  createdAt: string
}

/** Same-origin in the LAN deployment; proxied by Vite during development. */
const BASE = import.meta.env.VITE_API_BASE ?? '/api'

class HttpError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await apiFetch(`${BASE}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...init?.headers,
    },
  })

  if (!response.ok) {
    const detail = await response.text().catch(() => '')
    throw new HttpError(response.status, detail || response.statusText)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return response.json() as Promise<T>
}

const query = (params: Record<string, string | undefined>): string => {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value) search.set(key, value)
  }
  const encoded = search.toString()
  return encoded ? `?${encoded}` : ''
}

export const httpCatalogRepository: CatalogRepository = {
  listFeed(topic, pageToken) {
    // "All" is the client-side label for no filter; the API takes an empty value.
    const value = topic === 'All' ? undefined : topic
    return request<Feed>(`/feed${query({ topic: value, pageToken })}`)
  },

  getVideo(id) {
    return request<Video>(`/videos/${encodeURIComponent(id)}`)
  },

  async getVideoEnsuring(id) {
    try {
      return await request<Video>(`/videos/${encodeURIComponent(id)}`)
    } catch (error) {
      if (!(error instanceof HttpError) || error.status !== 404) throw error
      // The id is a YouTube id everywhere it can reach this point, so the
      // watch URL is reconstructable and no caller has to carry a source URL
      // through a queue or a shared link just to make this work.
      await request<{ videoId: string }>('/videos/external', {
        method: 'POST',
        body: JSON.stringify({ url: `https://www.youtube.com/watch?v=${id}` }),
      })
      return await request<Video>(`/videos/${encodeURIComponent(id)}`)
    }
  },

  async listUpNext(currentVideoId, channelFilter, pageToken, pageSize) {
    return request<Feed>(
      `/videos/${encodeURIComponent(currentVideoId)}/up-next${query({ channel: channelFilter, pageToken, pageSize: pageSize?.toString() })}`,
    )
  },

  listComments(videoId, pageToken, pageSize) {
    const params = new URLSearchParams()
    if (pageToken) params.set('pageToken', pageToken)
    if (pageSize) params.set('pageSize', String(pageSize))
    const qs = params.toString()
    return request<CommentPage>(`/videos/${encodeURIComponent(videoId)}/comments${qs ? '?' + qs : ''}`)
  },

  async listTopics() {
    const { topics } = await request<{ topics: Topic[] }>('/topics')
    return topics
  },

  refreshTopics() {
    return request<ScanStatus>('/topics/refresh', { method: 'POST' })
  },

  getScanStatus() {
    return request<ScanStatus>('/topics/scan-status')
  },

  search(q, pageToken) {
    return request<Feed>(`/search${query({ q, pageToken })}`)
  },

  async suggest(q) {
    const { suggestions } = await request<{ suggestions: Suggestion[] }>(
      `/suggest${query({ q })}`,
    )
    return suggestions
  },

  async resolveChannel(q) {
    if (!q.trim()) return null
    const { channel } = await request<{ channel: string | null }>(
      `/channels/resolve${query({ q })}`,
    )
    return channel
  },

  async discover(q, limit) {
    if (!q.trim()) return []
    // Upstream search has no cursor: asking for more means asking for a larger
    // page and taking the extra results.
    const { videos } = await request<{ videos: ExternalVideo[] }>(
      `/discover${query({ q, limit: String(limit) })}`,
    )
    return videos
  },

  async ensureExternal(sourceUrl) {
    const { videoId } = await request<{ videoId: string }>('/videos/external', {
      method: 'POST',
      body: JSON.stringify({ url: sourceUrl }),
    })
    return videoId
  },

  getStorage() {
    return request<StorageUsage>('/storage')
  },

  listPinned(pageToken) {
    return request<Feed>(`/pinned${query({ pageToken })}`)
  },

  async setPinned(videoId, pinned) {
    await request<void>(`/videos/${encodeURIComponent(videoId)}/pinned`, {
      method: 'POST',
      body: JSON.stringify({ pinned }),
    })
  },

  listPlaylists() {
    return request<{ playlists: Playlist[] }>('/playlists').then((r) => r.playlists)
  },

  getPlaylist(id, pageToken) {
    return request<{ playlist: Playlist; videos: Video[]; nextPageToken?: string }>(
      `/playlists/${encodeURIComponent(id)}${query({ pageToken })}`,
    )
  },





  listWatchLater(pageToken) {
    return request<Feed>(`/watch-later${query({ pageToken })}`)
  },


  listHistory(pageToken) {
    return request<Feed>(`/history${query({ pageToken })}`)
  },

  getStream(videoId, prefetch) {
    return request<StreamSources>(
      `/videos/${encodeURIComponent(videoId)}/stream${prefetch ? '?prefetch=1' : ''}`,
    )
  },

  async cancelDownload(videoId) {
    await request<{ cancelled: number }>(
      `/videos/${encodeURIComponent(videoId)}/download/cancel`,
      { method: 'POST' },
    )
  },

  async cancelJob(jobId: string) {
    await request<void>(`/ingest/jobs/${encodeURIComponent(jobId)}/cancel`, { method: 'POST' })
  },

  async dismissJob(jobId: string) {
    await request<void>(`/ingest/jobs/${encodeURIComponent(jobId)}/dismiss`, { method: 'POST' })
  },

  async retryJob(jobId: string) {
    return request<IngestJob>(`/ingest/jobs/${encodeURIComponent(jobId)}/retry`, {
      method: 'POST',
    })
  },

  async listScans(limit, offset) {
    return request<ScanPage>(`/scans${query({ limit: String(limit), offset: String(offset) })}`)
  },

  async clearScans() {
    await request<void>('/scans/clear', { method: 'POST' })
  },

  async dismissJobs(state) {
    const body = await request<{ dismissed: number }>('/ingest/dismiss-jobs', {
      method: 'POST',
      body: JSON.stringify({ state }),
    })
    return body.dismissed
  },

  async listJobs(activeOnly, options) {
    const { jobs } = await request<{ jobs: IngestJob[] }>(
      `/ingest/jobs${query({
        activeOnly: activeOnly ? 'true' : undefined,
        hideDismissed: options?.hideDismissed ? 'true' : undefined,
        limit: options?.limit ? String(options.limit) : undefined,
      })}`,
    )
    return jobs
  },

  recordProgress(videoId, positionSeconds, watchedFraction) {
    return request<void>(`/videos/${encodeURIComponent(videoId)}/progress`, {
      method: 'POST',
      body: JSON.stringify({ positionSeconds, watchedFraction }),
    })
  },

  recordNotInterested(videoId) {
    return request<void>(`/videos/${encodeURIComponent(videoId)}/not-interested`, {
      method: 'POST',
    })
  },

  async setReaction(videoId, reaction) {
    const { likeCount } = await request<{ likeCount: number }>(
      `/videos/${encodeURIComponent(videoId)}/reaction`,
      { method: 'POST', body: JSON.stringify({ reaction }) },
    )
    return likeCount
  },

  fetchComments(videoId) {
    return request<FetchCommentsResult>(`/videos/${encodeURIComponent(videoId)}/comments/fetch`, { method: 'POST' })
  },

  addComment(videoId, text, parentCommentId) {
    return request<Comment>(`/videos/${encodeURIComponent(videoId)}/comments`, {
      method: 'POST',
      body: JSON.stringify({ text, parentCommentId }),
    })
  },

  getChannel(channelId) {
    return request<ChannelPage>(`/channels/${encodeURIComponent(channelId)}`)
  },

  listChannelVideos(channelId, pageToken) {
    return request<ChannelUploads>(
      `/channels/${encodeURIComponent(channelId)}/videos${query({ pageToken })}`,
    )
  },

  setSubscription(channelId, subscribed) {
    return request<void>(`/channels/${encodeURIComponent(channelId)}/subscription`, {
      method: 'POST',
      body: JSON.stringify({ subscribed }),
    })
  },

  async listSubscriptions() {
    const { channels } = await request<{ channels: Channel[] }>('/subscriptions')
    return channels
  },

  async listTopPlayed(limit) {
    const { videos } = await request<Feed>(
      `/collections/top-played${query({ limit: limit ? String(limit) : undefined })}`,
    )
    return videos
  },

  async listPopular(limit) {
    const { videos } = await request<Feed>(
      `/collections/popular${query({ limit: limit ? String(limit) : undefined })}`,
    )
    return videos
  },
}
