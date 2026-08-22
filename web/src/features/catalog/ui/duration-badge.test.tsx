import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { VideoCard } from './VideoCard'
import type { Video } from '../domain/video'

/**
 * A card says nothing about length when it does not know the length.
 *
 * Flat listings carry no duration, so a great many rows arrive with zero — and
 * "0:00" is not a shorter way of saying "we don't know", it is a claim that the
 * video is empty.
 *
 * The rule already existed. ExternalVideoCard and QueueRail both drew nothing
 * in that case while this card and the up next rail printed the zero, which is
 * the sort of disagreement nobody notices until they are looking at two of them
 * at once.
 */

const base: Video = {
  id: 'v1',
  title: 'A video',
  channel: {
    id: 'c1', name: 'A channel', handle: '@a', avatarPath: '', bannerPath: '',
    subscriberCount: 0, verified: false, subscribed: false,
  },
  durationSeconds: 0,
  viewCount: 0,
  publishedAt: '',
  addedAt: new Date().toISOString(),
  thumbnailPath: '',
  description: '',
  hashtags: [],
  topics: [],
  mediaState: 'ABSENT',
  mediaPath: '',
  sizeBytes: 0,
  pinned: false,
  sourceUrl: '',
  likeCount: 0,
  subtitles: [],
}

function show(video: Video) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <VideoCard video={video} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('the duration badge', () => {
  it('is absent when the length is not known', () => {
    show(base)
    expect(screen.queryByText('0:00')).toBeNull()
  })

  it('is there when the length is known', () => {
    show({ ...base, durationSeconds: 641 })
    expect(screen.getByText('10:41')).toBeInTheDocument()
  })

  /**
   * The exception that made the fault visible: a broadcast carries zero and
   * still has something to say in that corner.
   */
  it('says LIVE on a broadcast, which also has no duration', () => {
    show({ ...base, isLiveNow: true })
    expect(screen.getByText('LIVE')).toBeInTheDocument()
    expect(screen.queryByText('0:00')).toBeNull()
  })
})
