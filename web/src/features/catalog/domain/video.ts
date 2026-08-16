/**
 * Domain layer — pure entities.
 * Must not import React and must not know that HTTP or a database exist.
 *
 * Shapes mirror the gateway contract, which is itself generated from
 * proto/catalog/v1/catalog.proto.
 */

export interface Channel {
  id: string
  name: string
  handle: string
  avatarPath: string
  bannerPath: string
  subscriberCount: number
  verified: boolean
  subscribed: boolean
}

/** State of the media file on disk. Tied to the LRU eviction policy. */
export type MediaState =
  | 'QUEUED'
  | 'DOWNLOADING'
  | 'READY'
  | 'EVICTED'
  | 'FAILED'
  // Upstream will not hand it over: members-only, private, removed. Apart from
  // FAILED because there is nothing to retry — an offer to try again is what
  // turned one members-only video into thirteen download jobs in two minutes.
  | 'UNAVAILABLE'

export type ReactionState = 'NONE' | 'LIKE' | 'DISLIKE'

export interface VideoUserState {
  /** Watch position in the 0..1 range. */
  watchProgress: number
  watchPositionSeconds: number
  reaction: ReactionState
  inWatchLater: boolean
}

/** Why the recommendation service surfaced a video. Empty outside feeds. */
export type RecommendationReason =
  | 'CONTINUE_WATCHING'
  | 'RECENTLY_ADDED'
  | 'NEVER_WATCHED'
  | 'SUBSCRIBED_CHANNEL'
  | 'REWATCH'
  | 'BOUNCED'
  | 'DISCOVERY'
  | 'SAME_CHANNEL'
  | 'SHARED_TAGS'
  | ''

export interface SubtitleTrack {
  language: string
  label: string
  url: string
  /** Machine generated captions are noticeably worse; the UI says so. */
  generated: boolean
}

export interface Video {
  id: string
  title: string
  channel: Channel
  durationSeconds: number
  viewCount: number
  publishedAt: string // ISO 8601
  /** When the video was ingested into the local library. */
  addedAt: string // ISO 8601
  thumbnailPath: string
  description: string
  hashtags: string[]
  /** Topic names this video was discovered under, from topics.yaml. */
  topics: string[]
  mediaState: MediaState
  mediaPath: string
  sizeBytes: number
  pinned: boolean
  sourceUrl: string
  /** Aggregate across all local users. */
  likeCount: number
  subtitles: SubtitleTrack[]
  userState?: VideoUserState
  reason?: RecommendationReason
}

export interface Comment {
  id: string
  authorHandle: string
  avatarPath: string
  text: string
  publishedAt: string
  likeCount: number
  pinnedBy?: string
  replies: Comment[]
}

export interface Topic {
  name: string
  videoCount: number
}

/** One member's collection. Watch later is deliberately not one of these. */
export interface Playlist {
  id: string
  title: string
  description: string
  itemCount: number
  /** The YouTube playlist it was imported from. Absent when made here. */
  sourceUrl?: string
  updatedAt: string
  /** False until this playlist's contents have been read from YouTube. */
  itemsSynced: boolean
  thumbnails: string[]
}

export interface StorageUsage {
  usedBytes: number
  budgetBytes: number
  diskFreeBytes: number
  videoCount: number
  evictedCount: number
  /** Downloaded videos somebody in the household has saved, so the sweep leaves them. */
  keptCount: number
  evictionCandidates: Video[]
}

export const watchProgress = (v: Video): number => v.userState?.watchProgress ?? 0
export const isWatched = (v: Video): boolean => watchProgress(v) > 0.95
export const isInProgress = (v: Video): boolean =>
  watchProgress(v) > 0.02 && watchProgress(v) <= 0.95
export const hasProgress = (v: Video): boolean => watchProgress(v) > 0
