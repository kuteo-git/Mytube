import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, within } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import type { Video } from '../domain/video'
import { VideoCard, type VideoCardVariant } from './VideoCard'

const video: Video = {
  id: 'vid1',
  title: 'A video',
  channel: {
    id: 'UCchannel',
    name: 'The Slow Mo Guys',
    handle: '@theslowmoguys',
    avatarPath: '',
    bannerPath: '',
    subscriberCount: 1,
    verified: false,
    subscribed: false,
  },
  durationSeconds: 100,
  viewCount: 10,
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

function renderCard(variant?: VideoCardVariant) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <VideoCard video={video} variant={variant} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('a video card', () => {
  it('takes you to the channel from the picture as well as the name', () => {
    // The avatar was the one half of the byline that did nothing — a channel's
    // face sitting beside a link bearing that channel's name reads as the same
    // control, so only one of them working is a control that half-works.
    renderCard()

    const toChannel = screen
      .getAllByRole('link')
      .filter((a) => a.getAttribute('href') === '/channel/UCchannel')

    expect(toChannel).toHaveLength(2)
    // One is reached by the name, the other by the picture — and the picture's
    // has to name the channel for anyone not looking at it.
    expect(toChannel.some((a) => within(a).queryByText('The Slow Mo Guys'))).toBe(true)
    expect(toChannel.some((a) => a.getAttribute('aria-label') === 'The Slow Mo Guys')).toBe(true)
  })

  it('still opens the video from the title and the thumbnail', () => {
    // The card's own job, which the channel links sit inside and must not take
    // over: three targets on one card, two destinations.
    renderCard()

    const toWatch = screen
      .getAllByRole('link', { hidden: true })
      .filter((a) => a.getAttribute('href') === '/watch/vid1')

    expect(toWatch.length).toBeGreaterThanOrEqual(2)
  })
})

describe('card menu by variant', () => {
  const openMenu = () => {
    fireEvent.click(screen.getByLabelText('More options'))
  }

  it('continueWatching shows Watched and Not interested', () => {
    renderCard('continueWatching')
    openMenu()
    expect(screen.getByText('Watched')).toBeInTheDocument()
    expect(screen.getByText('Not interested')).toBeInTheDocument()
    expect(screen.queryByText('Save')).not.toBeInTheDocument()
    expect(screen.queryByText('Unsave')).not.toBeInTheDocument()
  })

  it('feed shows Watched, Save, and Not interested', () => {
    renderCard('feed')
    openMenu()
    expect(screen.getByText('Watched')).toBeInTheDocument()
    expect(screen.getByText('Save')).toBeInTheDocument()
    expect(screen.getByText('Not interested')).toBeInTheDocument()
  })

  it('saved shows Unsave', () => {
    renderCard('saved')
    openMenu()
    expect(screen.getByText('Unsave')).toBeInTheDocument()
    expect(screen.queryByText('Save')).not.toBeInTheDocument()
    expect(screen.queryByText('Watched')).not.toBeInTheDocument()
  })

  it('history shows Save', () => {
    renderCard('history')
    openMenu()
    expect(screen.getByText('Save')).toBeInTheDocument()
    expect(screen.queryByText('Watched')).not.toBeInTheDocument()
    expect(screen.queryByText('Not interested')).not.toBeInTheDocument()
  })

  // Putting a video aside and keeping its bytes are two intentions, so a card
  // offers both and never conflates them.
  it('feed offers Watch later alongside Save', () => {
    renderCard('feed')
    openMenu()
    expect(screen.getByText('Watch later')).toBeInTheDocument()
    expect(screen.getByText('Save')).toBeInTheDocument()
  })

  it('watchLater shows Remove from Watch later, and still offers Save', () => {
    renderCard('watchLater')
    openMenu()
    expect(screen.getByText('Remove from Watch later')).toBeInTheDocument()
    expect(screen.queryByText('Watch later')).not.toBeInTheDocument()
    expect(screen.getByText('Save')).toBeInTheDocument()
  })

  it('storage shows Save', () => {
    renderCard('storage')
    openMenu()
    expect(screen.getByText('Save')).toBeInTheDocument()
    expect(screen.queryByText('Watched')).not.toBeInTheDocument()
    expect(screen.queryByText('Not interested')).not.toBeInTheDocument()
  })
})
