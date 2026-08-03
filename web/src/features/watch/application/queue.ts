import { useEffect, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useChannel, useChannelVideos, useTopPlayed } from '@/features/catalog/application/queries'
import { mediaURL } from '@/shared/lib/media'

/**
 * The playback queue: an ordered list the watch page is playing through.
 *
 * Carried in the URL rather than in memory, the way youtube.com carries `list`.
 * That is what makes a queue survive a reload, a shared link and the back
 * button — none of which a React state would.
 *
 * Two kinds exist so far, and both reuse the query that already fetched the
 * list, so opening a video from a channel costs no extra request:
 *
 *   ?list=channel:<channelId>&sort=<token>   the channel's videos, in the order
 *                                            the channel page was showing them
 *   ?list=top-played                         the most-watched collection
 */
export const CHANNEL_LIST_PREFIX = 'channel:'
export const TOP_PLAYED_LIST = 'top-played'

/** Videos left to play before the next page of a channel is pulled in. */
const QUEUE_PREFETCH_MARGIN = 3

/**
 * One row of the queue. Carries what a row has to render — a queue rail that
 * only knew titles would be a wall of text beside a page made of thumbnails.
 *
 * The two list kinds describe a video differently (a channel listing comes
 * straight from YouTube, the top-played collection from the catalog), so both
 * are flattened into this shape at the edge rather than leaking upward.
 */
export interface QueueItem {
  id: string
  title: string
  channelName: string
  thumbnailUrl: string
  durationSeconds: number
  /** ISO date, or '' when the source did not give one. */
  publishedAt: string
}

export interface Queue {
  /** Empty when the video was not opened from a list. */
  items: QueueItem[]
  /** Position of the video being watched, or -1 when it is not in the list. */
  currentIndex: number
  next?: QueueItem
  /** Query string to append to a link so the next video keeps the queue. */
  search: string
}

/** Builds the `?list=…` search string for a channel's videos. */
export function channelQueueSearch(channelId: string, sortToken?: string): string {
  const params = new URLSearchParams({ list: CHANNEL_LIST_PREFIX + channelId })
  if (sortToken) params.set('sort', sortToken)
  return `?${params.toString()}`
}

export function topPlayedQueueSearch(): string {
  return `?list=${TOP_PLAYED_LIST}`
}

export function useQueue(currentVideoId: string | undefined): Queue {
  const [params] = useSearchParams()
  const list = params.get('list') ?? ''
  const sort = params.get('sort') ?? undefined

  const channelId = list.startsWith(CHANNEL_LIST_PREFIX)
    ? list.slice(CHANNEL_LIST_PREFIX.length)
    : undefined

  // Both hooks are called unconditionally — hooks must be — but each is
  // disabled unless its kind of list is the one in the URL, so only the
  // relevant request runs. The channel query shares its cache key with the
  // channel page, so arriving from there costs nothing.
  const channelQuery = useChannelVideos(channelId, sort)
  const topPlayedQuery = useTopPlayed(50, list === TOP_PLAYED_LIST)
  // A channel listing does not repeat the channel name on every entry, so it
  // comes from the channel itself. Shares its cache key with the channel page.
  const channelQueryInfo = useChannel(channelId)
  const channelName = channelQueryInfo.data?.channel.name ?? ''

  const queue = useMemo(() => {
    let items: QueueItem[] = []
    if (channelId) {
      items =
        channelQuery.data?.pages.flatMap((page) =>
          page.videos.map((v) => ({
            id: v.id,
            title: v.title,
            channelName: v.channelName || channelName,
            thumbnailUrl: v.thumbnailUrl,
            durationSeconds: v.durationSeconds,
            publishedAt: v.publishedAt ?? '',
          })),
        ) ?? []
    } else if (list === TOP_PLAYED_LIST) {
      items = (topPlayedQuery.data ?? []).map((v) => ({
        id: v.id,
        title: v.title,
        channelName: v.channel.name,
        thumbnailUrl: mediaURL(v.thumbnailPath) ?? '',
        durationSeconds: v.durationSeconds,
        publishedAt: v.publishedAt ?? '',
      }))
    }

    const currentIndex = currentVideoId ? items.findIndex((i) => i.id === currentVideoId) : -1
    // Only a video actually found in the list has a next one. A queue that
    // guessed would send the viewer somewhere unrelated.
    const next = currentIndex >= 0 ? items[currentIndex + 1] : undefined

    const search = list
      ? `?${new URLSearchParams(sort ? { list, sort } : { list }).toString()}`
      : ''

    return { items, currentIndex, next, search }
  }, [channelId, channelName, list, sort, currentVideoId, channelQuery.data, topPlayedQuery.data])

  // A channel arrives one page at a time, so playing towards the bottom of the
  // loaded part would otherwise run out of queue — "next" would vanish
  // mid-channel and the rail would look like the channel had ended. Pull the
  // following page while there is still something to play from this one.
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = channelQuery
  const remaining = queue.currentIndex >= 0 ? queue.items.length - queue.currentIndex - 1 : -1
  useEffect(() => {
    if (!channelId || remaining < 0 || remaining > QUEUE_PREFETCH_MARGIN) return
    if (!hasNextPage || isFetchingNextPage) return
    void fetchNextPage()
  }, [channelId, remaining, hasNextPage, isFetchingNextPage, fetchNextPage])

  return queue
}
