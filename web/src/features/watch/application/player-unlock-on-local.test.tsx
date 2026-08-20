import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'

/**
 * The local file landing unlocks a player that had given up.
 *
 * `playable` is `frontSrc && !loadFailed`, and nothing but navigating to
 * another video ever cleared `loadFailed`. That was survivable while a failed
 * muxed stream could retreat to a progressive one; it stopped being survivable
 * on 2026-08-18, when the progressive rendition was measured to serve the head
 * of the file and refuse the middle — 403 on 12 of 14 videos, 206 never — and
 * stopped being offered at all. The muxed stream became the only source before
 * the copy lands.
 *
 * So a mux that upstream refuses now ends the video for as long as the page
 * stays open, while the download beside it finishes in a median of thirteen
 * seconds. What the viewer sees: "The stream could not be loaded" sitting over
 * a file that is already on the disk, and a reload as the only way out.
 */

const REMUX_URL = '/api/videos/abc/remux'
const LOCAL_URL = '/media/abc/1080p.mp4'

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

const video = {
  id: 'abc',
  title: 'A video whose mux upstream refused',
  channel,
  durationSeconds: 2400,
  viewCount: 10,
  publishedAt: new Date().toISOString(),
  addedAt: new Date().toISOString(),
  thumbnailPath: '',
  description: '',
  hashtags: [],
  topics: [],
  mediaState: 'ABSENT' as const,
  mediaPath: '',
  sizeBytes: 0,
  pinned: false,
  sourceUrl: '',
  likeCount: 0,
  subtitles: [],
  userState: {
    watchProgress: 0,
    watchPositionSeconds: 0,
    reaction: 'NONE' as const,
    inWatchLater: false,
  },
}

/** Flipped by the test once the download has landed. */
let onDisk = false

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getVideo: vi.fn(async () => video),
    getVideoEnsuring: vi.fn(async () => video),
    getStream: vi.fn(async () =>
      onDisk
        ? { local: { url: LOCAL_URL, name: 'local' }, instant: null, remux: null }
        : { local: null, instant: null, remux: { url: REMUX_URL, height: 720, name: 'remux' } },
    ),
    getRemuxStart: vi.fn(async (_id: string, at: number) => Math.max(0, at - 2.028)),
    listUpNext: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    listPopular: vi.fn(async () => []),
    listComments: vi.fn(async () => ({ comments: [], nextPageToken: '' })),
    listTopics: vi.fn(async () => []),
    listSubscriptions: vi.fn(async () => []),
    listJobs: vi.fn(async () => []),
    listFeed: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    recordProgress: vi.fn(async () => {}),
    cancelDownload: vi.fn(async () => {}),
    getStorage: vi.fn(async () => ({ usedBytes: 0, budgetBytes: 1 })),
  },
}))

const settle = (ms = 20) => act(async () => void (await new Promise((r) => setTimeout(r, ms))))

let client: QueryClient

beforeEach(() => {
  window.localStorage.clear()
  onDisk = false
})

function visible(videos: NodeListOf<HTMLVideoElement>) {
  return Array.from(videos).find((v) => v.getAttribute('aria-hidden') !== 'true') ?? videos[0]
}

async function mounted() {
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/watch/abc']}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<div>home</div>} />
            <Route path="/watch/:videoId" element={<WatchPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  await waitFor(() => expect(document.querySelectorAll('video').length).toBe(2))
  await settle()
  return document.querySelectorAll('video') as NodeListOf<HTMLVideoElement>
}

it('plays the downloaded file after the only live tier was refused', async () => {
  const videos = await mounted()
  await waitFor(() => expect(visible(videos).src).toContain('/remux'))

  // Upstream refuses the mux, twice: the first failure is retried by asking for
  // the stream again, and the second is the one that gives up.
  await act(async () => {
    fireEvent.error(visible(videos))
  })
  await settle(50)
  await act(async () => {
    const again = document.querySelectorAll('video')
    if (again.length > 0) fireEvent.error(visible(again as NodeListOf<HTMLVideoElement>))
  })
  await settle(50)

  // Given up: no element left to play, and a sentence in its place.
  await waitFor(() => expect(document.querySelectorAll('video').length).toBe(0))

  // The download lands, which is the one thing here that has never failed.
  onDisk = true
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['stream', 'abc'] })
  })
  await settle(50)

  const back = await waitFor(() => {
    const all = document.querySelectorAll('video')
    expect(all.length).toBe(2)
    return all as NodeListOf<HTMLVideoElement>
  })
  await waitFor(() => expect(visible(back).src).toContain(LOCAL_URL))
})
