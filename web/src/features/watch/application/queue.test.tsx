import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import {
  playlistQueueSearch,
  useQueue,
  watchLaterQueueSearch,
} from '@/features/watch/application/queue'
import type { Video } from '@/features/catalog/domain/video'

function video(id: string, title: string): Video {
  return {
    id,
    title,
    channel: {
      id: 'UCc',
      name: 'A channel',
      handle: '',
      avatarPath: '',
      bannerPath: '',
      subscriberCount: 0,
      verified: false,
      subscribed: false,
    },
    durationSeconds: 10,
    viewCount: 0,
    publishedAt: '2026-01-01T00:00:00Z',
    addedAt: '2026-01-01T00:00:00Z',
    thumbnailPath: '',
    description: '',
    hashtags: [],
    topics: [],
    mediaState: 'READY',
    mediaPath: '',
    sizeBytes: 0,
    pinned: false,
    sourceUrl: '',
    likeCount: 0,
    subtitles: [],
  }
}

const playlistPage = {
  playlist: {
    id: 'pl_1',
    title: 'Luke Music',
    description: '',
    itemCount: 3,
    updatedAt: '',
    itemsSynced: true,
    unavailable: false,
    thumbnails: [],
  },
  videos: [video('a', 'One'), video('b', 'Two'), video('c', 'Three')],
}

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getPlaylist: vi.fn(async () => playlistPage),
    listWatchLater: vi.fn(async () => ({
      videos: [video('x', 'Later one'), video('y', 'Later two')],
    })),
    listChannelVideos: vi.fn(async () => ({ videos: [] })),
    getChannel: vi.fn(async () => ({ channel: {}, videoCount: 0 })),
    listTopPlayed: vi.fn(async () => []),
  },
}))

function renderQueue(search: string, videoId: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return renderHook(() => useQueue(videoId), {
    wrapper: ({ children }) => (
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={[`/watch/${videoId}${search}`]}>{children}</MemoryRouter>
      </QueryClientProvider>
    ),
  })
}

// A playlist that cannot be played through is only a way of finding something
// to leave it by: next has to be the next entry, not a recommendation.
describe('playing through a playlist', () => {
  it('makes the next entry the next video', async () => {
    const { result } = renderQueue(playlistQueueSearch('pl_1'), 'b')

    await waitFor(() => expect(result.current.items).toHaveLength(3))
    expect(result.current.currentIndex).toBe(1)
    expect(result.current.next?.id).toBe('c')
    // And the link the rail builds keeps the list, or the queue would end after
    // one hop.
    expect(result.current.search).toBe('?list=playlist%3Apl_1')
  })

  it('names the playlist rather than saying "queue"', async () => {
    const { result } = renderQueue(playlistQueueSearch('pl_1'), 'a')
    await waitFor(() => expect(result.current.label).toBe('Luke Music'))
  })

  it('has no next after the last entry', async () => {
    const { result } = renderQueue(playlistQueueSearch('pl_1'), 'c')
    await waitFor(() => expect(result.current.items).toHaveLength(3))
    expect(result.current.next).toBeUndefined()
  })
})

// Watch later is a list like any other, and was the one that behaved like a
// page of unrelated links.
describe('playing through Watch later', () => {
  it('plays the list in order', async () => {
    const { result } = renderQueue(watchLaterQueueSearch(), 'x')

    await waitFor(() => expect(result.current.items).toHaveLength(2))
    expect(result.current.next?.id).toBe('y')
    expect(result.current.label).toBe('Watch later')
  })
})

// A video opened on its own still has no queue, which is what keeps the
// recommendation rail on the pages that want it.
describe('a video opened on its own', () => {
  it('has no queue at all', async () => {
    const { result } = renderQueue('', 'a')
    expect(result.current.items).toHaveLength(0)
    expect(result.current.next).toBeUndefined()
    expect(result.current.search).toBe('')
  })
})
