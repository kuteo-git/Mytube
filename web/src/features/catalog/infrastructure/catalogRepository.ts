import type {
  Channel,
  Topic,
  Comment,
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
  listUpNext(currentVideoId: string, channelFilter?: string): Promise<Video[]>
  listComments(videoId: string): Promise<CommentPage>
  listTopics(): Promise<Topic[]>
  refreshTopics(): Promise<ScanStatus>
  getScanStatus(): Promise<ScanStatus>
  search(query: string, pageToken?: string): Promise<Feed>
  suggest(query: string): Promise<Suggestion[]>
  discover(query: string, limit: number): Promise<ExternalVideo[]>
  ensureExternal(sourceUrl: string): Promise<string>
  getStorage(): Promise<StorageUsage>

  /**
   * Asks how to play a video. The answer is either the local file or a
   * short-lived upstream URL while the copy downloads, so the UI never has to
   * know which half of the hybrid model it is in.
   */
  getStream(videoId: string): Promise<StreamSource>
  listJobs(activeOnly: boolean): Promise<IngestJob[]>

  recordProgress(videoId: string, positionSeconds: number, watchedFraction: number): Promise<void>
  setReaction(videoId: string, reaction: ReactionState): Promise<number>
  addComment(videoId: string, text: string, parentCommentId?: string): Promise<Comment>

  getChannel(channelId: string): Promise<ChannelPage>
  listChannelVideos(channelId: string, pageToken?: string): Promise<Feed>
  setSubscription(channelId: string, subscribed: boolean): Promise<void>
  listSubscriptions(): Promise<Channel[]>
}

export interface ChannelPage {
  channel: Channel
  videoCount: number
}

export interface Feed {
  videos: Video[]
  nextPageToken?: string
}

export interface CommentPage {
  comments: Comment[]
  totalCount: number
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
  inLibrary: boolean
}

export interface Suggestion {
  text: string
  kind: 'TITLE' | 'TOPIC' | 'CHANNEL'
  videoCount: number
}

export interface ScanStatus {
  startedAt: string
  durationMs: number
  sourcesScanned: number
  sourcesFailed: number
  videosSeen: number
  videosAdded: number
  errors: string[]
}

export interface StreamSource {
  source: 'local' | 'upstream'
  url: string
  height?: number
  mimeType?: string
  expiresAt?: string
}

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
  const response = await fetch(`${BASE}${path}`, {
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

  async listUpNext(currentVideoId, channelFilter) {
    const feed = await request<Feed>(
      `/videos/${encodeURIComponent(currentVideoId)}/up-next${query({ channel: channelFilter })}`,
    )
    return feed.videos
  },

  listComments(videoId) {
    return request<CommentPage>(`/videos/${encodeURIComponent(videoId)}/comments`)
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

  getStream(videoId) {
    return request<StreamSource>(`/videos/${encodeURIComponent(videoId)}/stream`)
  },

  async listJobs(activeOnly) {
    const { jobs } = await request<{ jobs: IngestJob[] }>(
      `/ingest/jobs${query({ activeOnly: activeOnly ? 'true' : undefined })}`,
    )
    return jobs
  },

  recordProgress(videoId, positionSeconds, watchedFraction) {
    return request<void>(`/videos/${encodeURIComponent(videoId)}/progress`, {
      method: 'POST',
      body: JSON.stringify({ positionSeconds, watchedFraction }),
    })
  },

  async setReaction(videoId, reaction) {
    const { likeCount } = await request<{ likeCount: number }>(
      `/videos/${encodeURIComponent(videoId)}/reaction`,
      { method: 'POST', body: JSON.stringify({ reaction }) },
    )
    return likeCount
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
    return request<Feed>(`/channels/${encodeURIComponent(channelId)}/videos${query({ pageToken })}`)
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
}
