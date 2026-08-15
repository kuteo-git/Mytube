import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'

/**
 * A video YouTube will not hand over — members-only, private, removed.
 *
 * It reached the viewer as a blank picture beside a 500 on /comments/fetch and
 * a 502 on /remux, because nothing in the library distinguished "not fetched
 * yet" from "will never be fetched". Both of those requests were also the
 * library asking upstream a question it had already been refused.
 */

const channel = {
  id: 'c1',
  name: 'A channel',
  handle: '@a',
  avatarPath: '',
  bannerPath: '',
  subscriberCount: 0,
  verified: false,
  subscribed: false,
}

/** Set per test. */
let mediaState: string = 'UNAVAILABLE'

const video = () => ({
  id: 'abc',
  title: 'A video nobody outside can fetch',
  channel,
  durationSeconds: 600,
  viewCount: 10,
  publishedAt: new Date().toISOString(),
  addedAt: new Date().toISOString(),
  thumbnailPath: '',
  description: '',
  hashtags: [],
  topics: [],
  mediaState,
  mediaPath: '',
  sizeBytes: 0,
  pinned: false,
  sourceUrl: 'https://www.youtube.com/watch?v=abc',
  likeCount: 0,
  subtitles: [],
  userState: {
    watchProgress: 0,
    watchPositionSeconds: 0,
    reaction: 'NONE' as const,
    inWatchLater: false,
  },
})

const fetchComments = vi.fn(async () => ({ imported: 0, skipped: false }))

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getVideo: vi.fn(async () => video()),
    getVideoEnsuring: vi.fn(async () => video()),
    // What the gateway answers for a video upstream has refused: no sources at
    // all, and the reason in a word.
    getStream: vi.fn(async () => ({
      unavailable: { reason: 'members_only' as const },
    })),
    getRemuxStart: vi.fn(async () => 0),
    listUpNext: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    listPopular: vi.fn(async () => []),
    listComments: vi.fn(async () => ({ comments: [], nextPageToken: '' })),
    fetchComments: () => fetchComments(),
    listTopics: vi.fn(async () => []),
    listSubscriptions: vi.fn(async () => []),
    listJobs: vi.fn(async () => []),
    listFeed: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    recordProgress: vi.fn(async () => {}),
    cancelDownload: vi.fn(async () => {}),
    getStorage: vi.fn(async () => ({ usedBytes: 0, budgetBytes: 1 })),
  },
}))

const settle = (ms = 30) => act(async () => void (await new Promise((r) => setTimeout(r, ms))))

beforeEach(() => {
  mediaState = 'UNAVAILABLE'
  fetchComments.mockClear()
  window.localStorage.clear()
})

async function openWatchPage() {
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}
    >
      <MemoryRouter initialEntries={['/watch/abc']}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/watch/:videoId" element={<WatchPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  await waitFor(() => expect(screen.getByText('A video nobody outside can fetch')).toBeInTheDocument())
  await settle()
}

describe('a video upstream refuses', () => {
  it('says why, in words that lead somewhere', async () => {
    await openWatchPage()

    // The whole sentence, not just the words: what matters is that it names
    // the way in rather than reporting an error.
    expect(
      await screen.findByText(/Join the channel there to watch it/i),
    ).toBeInTheDocument()
  })

  // The reported 500. The section fetches comments the moment it finds none,
  // so leaving it mounted is a request per visit to an endpoint that can only
  // refuse — and refusing is exactly what it did, loudly.
  it('does not ask YouTube for comments', async () => {
    await openWatchPage()

    expect(fetchComments).not.toHaveBeenCalled()
    expect(screen.queryByLabelText('Comments')).not.toBeInTheDocument()
  })

  it('leaves comments alone for an ordinary video', async () => {
    mediaState = 'READY'
    await openWatchPage()

    expect(screen.getByLabelText('Comments')).toBeInTheDocument()
  })
})
