import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'

/**
 * Resuming a part-watched video on a stream that cannot be seeked.
 *
 * The muxed stream has no index, so it cannot be moved within — it has to be
 * *opened* where the viewer wants to be. That rule was written down in
 * CLAUDE.md §4 and enforced at three of the five places that move a playhead;
 * the place that restores a viewer's saved position was not one of them.
 *
 * It did not matter while videos opened on the progressive rendition, which
 * seeks like any file. It became the whole of "the video will not start" on the
 * day the mux became what every video opens on — and it fails silently, which
 * is why it took a week to find. A browser asked to seek an unindexed stream
 * does not refuse: it takes the number and buffers toward it, showing nothing.
 *
 * Measured on `ZIaOBAjvc38`, left at 336s: ingest logged the mux opening and
 * then closing 175ms later having delivered 3.6MB, with no error anywhere on
 * the server. The picture arrived when the *download* finished, 45 seconds
 * later, which is exactly what "phải đợi tải về xong mới play được" describes.
 */

const REMUX_URL = '/api/videos/abc/remux'

/** Where the viewer left off. The real number from the video that showed this. */
const RESUME_AT = 336

/** Where the server says a mux opened at `t` will really begin — a keyframe before it. */
const getRemuxStart = vi.fn(async (_id: string, at: number) => at - 1.8)

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
  title: 'A video watched before',
  channel,
  durationSeconds: 2400,
  viewCount: 10,
  publishedAt: new Date().toISOString(),
  addedAt: new Date().toISOString(),
  thumbnailPath: '',
  description: '',
  hashtags: [],
  topics: [],
  mediaState: 'READY' as const,
  mediaPath: '',
  sizeBytes: 0,
  pinned: false,
  sourceUrl: '',
  likeCount: 0,
  subtitles: [],
  userState: {
    watchProgress: 0.14,
    // The saved position. This is what the player restores on opening.
    watchPositionSeconds: RESUME_AT,
    reaction: 'NONE' as const,
    inWatchLater: false,
  },
}

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getVideo: vi.fn(async () => video),
    getVideoEnsuring: vi.fn(async () => video),
    // No local copy and no progressive rendition: since itag 18 stopped
    // serving, the mux is the only thing a video can open on, and it is the
    // one tier that cannot be seeked.
    getStream: vi.fn(async () => ({
      local: null,
      instant: null,
      remux: { url: REMUX_URL, height: 720, name: 'remux' },
    })),
    getRemuxStart: (id: string, at: number) => getRemuxStart(id, at),
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

beforeEach(() => {
  getRemuxStart.mockClear()
  window.localStorage.clear()
})

/**
 * Records every write to `currentTime` instead of applying it, so a test can
 * assert on what the player *tried* to do. A real browser accepts all of them
 * silently, which is the reason this bug was invisible.
 */
function watchPlayheads(elements: NodeListOf<HTMLVideoElement>) {
  const seeks: number[] = []
  for (const el of elements) {
    let clock = 0
    Object.defineProperty(el, 'buffered', {
      configurable: true,
      value: { length: 1, start: () => 0, end: () => 1e6 },
    })
    Object.defineProperty(el, 'currentTime', {
      configurable: true,
      get: () => clock,
      set: (v: number) => {
        seeks.push(v)
        clock = v
      },
    })
    Object.defineProperty(el, 'duration', { configurable: true, get: () => 2400 })
    Object.defineProperty(el, 'paused', { configurable: true, writable: true, value: false })
    Object.defineProperty(el, 'readyState', { configurable: true, get: () => 2 })
  }
  return seeks
}

async function mounted() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
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
  const videos = await waitFor(() => {
    const all = document.querySelectorAll('video')
    expect(all.length).toBe(2)
    return all as NodeListOf<HTMLVideoElement>
  })
  const seeks = watchPlayheads(videos)
  await settle()
  return { videos, seeks }
}

function visible(videos: NodeListOf<HTMLVideoElement>) {
  return Array.from(videos).find((v) => v.getAttribute('aria-hidden') !== 'true') ?? videos[0]
}

describe('resuming a video whose only stream cannot be seeked', () => {
  it('opens the stream at the saved position instead of seeking to it', async () => {
    const { videos } = await mounted()

    // The mark travels in the URL: the stream's own zero becomes where the
    // viewer left off, so there is nothing left to seek when it arrives.
    await waitFor(() => {
      expect(visible(videos).src).toContain('/remux')
      expect(visible(videos).src).toContain(`t=${RESUME_AT.toFixed(3)}`)
    })
  })

  it('never writes the playhead of the muxed stream', async () => {
    const { videos, seeks } = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain('/remux'))

    // The element reports its metadata, which is where the resume used to
    // happen — and where writing 336 bought minutes of blank picture.
    await act(async () => {
      visible(videos).dispatchEvent(new Event('loadedmetadata'))
    })
    await settle()

    expect(seeks).toEqual([])
  })

  /**
   * The stream begins at the keyframe before the mark, not at the mark, so the
   * player has to be told where its zero really sits — otherwise the seek bar
   * and every subtitle would be a second or two out for the whole video.
   */
  it('asks the server where the stream really begins', async () => {
    const { videos } = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain('/remux'))

    await waitFor(() => expect(getRemuxStart).toHaveBeenCalledWith('abc', RESUME_AT))
  })
})
