import type {
  Category,
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
  listFeed(category: string, pageToken?: string): Promise<Feed>
  getVideo(id: string): Promise<Video>
  listUpNext(currentVideoId: string, channelFilter?: string): Promise<Video[]>
  listComments(videoId: string): Promise<CommentPage>
  listCategories(): Promise<Category[]>
  search(query: string): Promise<Video[]>
  getStorage(): Promise<StorageUsage>

  /**
   * Asks how to play a video. The answer is either the local file or a
   * short-lived upstream URL while the copy downloads, so the UI never has to
   * know which half of the hybrid model it is in.
   */
  getStream(videoId: string): Promise<StreamSource>
  discover(query: string): Promise<ExternalVideo[]>
  previewUrl(url: string): Promise<ExternalVideo>
  submitIngest(url: string, preferredHeight?: number): Promise<IngestJob>
  listJobs(activeOnly: boolean): Promise<IngestJob[]>

  recordProgress(videoId: string, positionSeconds: number, watchedFraction: number): Promise<void>
  setReaction(videoId: string, reaction: ReactionState): Promise<number>
  setSubscription(channelId: string, subscribed: boolean): Promise<void>
  addComment(videoId: string, text: string, parentCommentId?: string): Promise<Comment>
}

export interface Feed {
  videos: Video[]
  nextPageToken?: string
}

export interface CommentPage {
  comments: Comment[]
  totalCount: number
}

export interface StreamSource {
  source: 'local' | 'upstream'
  url: string
  height?: number
  mimeType?: string
  expiresAt?: string
}

export interface ExternalVideo {
  id: string
  title: string
  channelId: string
  channelName: string
  durationSeconds: number
  viewCount: number
  thumbnailUrl: string
  sourceUrl: string
  publishedAt: string
  inLibrary: boolean
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
  listFeed(category, pageToken) {
    // "All" is the client-side label for no filter; the API takes an empty value.
    const value = category === 'All' ? undefined : category
    return request<Feed>(`/feed${query({ category: value, pageToken })}`)
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

  async listCategories() {
    const { categories } = await request<{ categories: Category[] }>('/categories')
    return categories
  },

  async search(q) {
    if (!q.trim()) return []
    const feed = await request<Feed>(`/search${query({ q })}`)
    return feed.videos
  },

  getStorage() {
    return request<StorageUsage>('/storage')
  },

  getStream(videoId) {
    return request<StreamSource>(`/videos/${encodeURIComponent(videoId)}/stream`)
  },

  async discover(q) {
    if (!q.trim()) return []
    const { videos } = await request<{ videos: ExternalVideo[] }>(`/discover${query({ q })}`)
    return videos
  },

  previewUrl(url) {
    return request<ExternalVideo>(`/discover/preview${query({ url })}`)
  },

  submitIngest(url, preferredHeight) {
    return request<IngestJob>('/ingest', {
      method: 'POST',
      body: JSON.stringify({ url, preferredHeight }),
    })
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

  setSubscription(channelId, subscribed) {
    return request<void>(`/channels/${encodeURIComponent(channelId)}/subscription`, {
      method: 'POST',
      body: JSON.stringify({ subscribed }),
    })
  },

  addComment(videoId, text, parentCommentId) {
    return request<Comment>(`/videos/${encodeURIComponent(videoId)}/comments`, {
      method: 'POST',
      body: JSON.stringify({ text, parentCommentId }),
    })
  },
}
