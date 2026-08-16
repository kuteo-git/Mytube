import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'

/**
 * Climbing away from a playhead that is not moving.
 *
 * The reported fault: the instant tier was refused upstream — googlevideo
 * answers 403 in waves, and during one of them the gateway's two attempts both
 * came back refused — so the front element never played a frame and sat paused
 * at zero. What the viewer then saw was the player flipping itself to 1080p,
 * the clock reading twenty seconds, and pressing play resuming twenty seconds
 * into a video they had not started. Reloading fixed it, because by then the
 * download had landed and the local file was the opening tier.
 *
 * The cause is not the 403. It is that the climb parks the replacement at
 * `position + 20s` — a lead that only makes sense while the playhead is running
 * — and then two separate comparisons read the resulting negative difference as
 * "both elements already agree on where they are" rather than "the replacement
 * begins after the viewer". A replacement that starts ahead of the viewer can
 * never be handed over as it stands: a muxed stream believes its mark is zero,
 * so there is nothing before it to wind back to.
 */

const REMUX_URL = '/api/videos/abc/remux'
const INSTANT_URL = '/api/videos/abc/instant'

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
  title: 'A video whose instant tier is refused',
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
    watchProgress: 0,
    watchPositionSeconds: 0,
    reaction: 'NONE' as const,
    inWatchLater: false,
  },
}

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getVideo: vi.fn(async () => video),
    getVideoEnsuring: vi.fn(async () => video),
    getStream: vi.fn(async () => ({
      local: null,
      instant: { url: INSTANT_URL, height: 360, name: 'instant' },
      remux: { url: REMUX_URL, height: 1080, name: 'remux' },
    })),
    // ffmpeg lands on the keyframe at or before the mark.
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
})

/**
 * jsdom has no media pipeline. These elements are paused at zero and stay
 * there, which is exactly the state a refused instant tier leaves the front in.
 */
function givePlayheads(elements: NodeListOf<HTMLVideoElement>) {
  const clock = new WeakMap<HTMLVideoElement, number>()
  for (const el of elements) {
    clock.set(el, 0)
    Object.defineProperty(el, 'buffered', {
      configurable: true,
      writable: true,
      value: { length: 1, start: () => 0, end: () => 1e6 },
    })
    Object.defineProperty(el, 'currentTime', {
      configurable: true,
      get: () => clock.get(el) ?? 0,
      set: (v: number) => clock.set(el, v),
    })
    Object.defineProperty(el, 'duration', { configurable: true, get: () => 2400 })
    Object.defineProperty(el, 'paused', { configurable: true, writable: true, value: true })
    Object.defineProperty(el, 'readyState', { configurable: true, get: () => 2 })
  }
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
  const videos = await waitFor(() => {
    const all = document.querySelectorAll('video')
    expect(all.length).toBe(2)
    return all as NodeListOf<HTMLVideoElement>
  })
  givePlayheads(videos)
  await settle()
  return videos
}

function visible(videos: NodeListOf<HTMLVideoElement>) {
  return Array.from(videos).find((v) => v.getAttribute('aria-hidden') !== 'true') ?? videos[0]
}

function hidden(videos: NodeListOf<HTMLVideoElement>) {
  return Array.from(videos).find((v) => v.getAttribute('aria-hidden') === 'true') ?? videos[1]
}

/** Where the viewer is told they are. */
function reportedPosition() {
  return Number((screen.getByLabelText('Seek') as HTMLInputElement).value)
}

/**
 * The instant tier being refused, as the element reports it: a decode error on
 * the front layer, after which its playhead never moves again.
 */
async function frontRefused(videos: NodeListOf<HTMLVideoElement>) {
  const el = visible(videos)
  Object.defineProperty(el, 'error', {
    configurable: true,
    value: { code: 3, message: 'PIPELINE_ERROR_READ: FFmpegDemuxer: data source error' },
  })
  await act(async () => {
    fireEvent.error(el)
  })
  await settle()
}

describe('a climb measured from a playhead that never moved', () => {
  it('opens the replacement where the viewer is, not twenty seconds ahead', async () => {
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain(INSTANT_URL))

    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
    await frontRefused(videos)

    // The lead exists so the mux is ready by the time the playhead arrives at
    // it. A dead playhead arrives nowhere, so there is nothing to lead, and the
    // claim measured from it before it died is not one to keep.
    await waitFor(() => {
      expect(hidden(videos).src).toContain('/remux')
      expect(hidden(videos).src).not.toMatch(/[?&]t=/)
    })
  })

  it('never winds the viewer forward when the replacement takes over', async () => {
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain(INSTANT_URL))
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
    await frontRefused(videos)

    // The prepared layer reports itself ready, which is what starts the
    // handover. Whatever the player decides here, the viewer is at zero and
    // must still be at zero afterwards.
    await act(async () => {
      fireEvent.loadedMetadata(hidden(videos))
    })
    await settle()
    // Whichever layer is in front now says where it is, and the offset of the
    // stream it is playing is added to that.
    await act(async () => {
      fireEvent.timeUpdate(visible(videos))
    })
    await settle()

    expect(reportedPosition()).toBeLessThanOrEqual(0.05)
  })

  it('still climbs normally from a playhead that is running', async () => {
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain(INSTANT_URL))

    // The ordinary case, which the fix must leave alone: the front is playing,
    // so the replacement is parked ahead and waits to be caught up with.
    Object.defineProperty(visible(videos), 'paused', { configurable: true, value: false })
    visible(videos).currentTime = 30
    await act(async () => {
      fireEvent.timeUpdate(visible(videos))
    })
    await settle()
    await act(async () => {
      fireEvent.loadedMetadata(hidden(videos))
    })
    await settle()

    // Handed over at the viewer's own position, never behind it.
    expect(reportedPosition()).toBeGreaterThanOrEqual(29.9)
  })
})
