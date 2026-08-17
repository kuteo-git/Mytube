import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'

/**
 * Retreating from a tier that broke after it was handed over.
 *
 * The reported sequence: the video opens at 360p, climbs to the muxed 1080p
 * stream, and stops — and pressing play does nothing at all until the page is
 * reloaded. Behind it, a mux whose audio input died a second in: the browser
 * rejects an audio packet with PIPELINE_ERROR_DECODE, which arrives here as an
 * `error` on the layer the viewer is watching.
 *
 * Everything was in place to recover except the one line that starts it.
 * `targetTier` already retreats — once `remuxFailed`, auto asks for the low
 * rendition again — but only a climb being abandoned ever counted a remux
 * failure. A remux that failed *after* being committed counted nothing, so the
 * player sat on a dead element with a working 360p source one step away.
 *
 * A failure on the front layer is also not the same as a video that cannot be
 * played: it only means *this* source cannot. Declaring the whole video broken
 * is what turned a recoverable stall into a reload.
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
  title: 'A video whose mux loses its audio',
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

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getVideo: vi.fn(async () => video),
    getVideoEnsuring: vi.fn(async () => video),
    getStream: vi.fn(async () =>
      remuxOnly
        ? { local: null, instant: null, remux: { url: REMUX_URL, height: 720, name: 'remux' } }
        : {
            local: null,
            instant: { url: INSTANT_URL, height: 360, name: 'instant' },
            remux: { url: REMUX_URL, height: 720, name: 'remux' },
          },
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
/** Set by a test: upstream published no progressive rendition worth serving. */
let remuxOnly = false

beforeEach(() => {
  window.localStorage.clear()
  remuxOnly = false
})

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
    Object.defineProperty(el, 'paused', { configurable: true, writable: true, value: false })
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

/** Climbs from the opening low rendition to the muxed stream, as auto does. */
async function climbToRemux(videos: NodeListOf<HTMLVideoElement>) {
  await waitFor(() => expect(visible(videos).src).toContain(INSTANT_URL))
  await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
  // Just past the mark the mux was opened at (20s, landing on the keyframe at
  // 17.972), not far past it: a stream that cannot be seeked is handed over
  // where it stands, so a viewer more than a second beyond it is given up on
  // rather than wound forward into an unindexed file.
  visible(videos).currentTime = 18.5
  await act(async () => {
    fireEvent.loadedMetadata(hidden(videos))
  })
  await settle()
  await waitFor(() => expect(visible(videos).src).toContain('/remux'))
}

describe('handing over to a stream that cannot be seeked', () => {
  it('never moves the playhead inside it', async () => {
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain(INSTANT_URL))
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))

    // The climb ran late: the viewer is already past the mark the muxed stream
    // was opened at. Catching the replacement up means seeking it — and a
    // fragmented MP4 arriving down a pipe carries no index, which is why the
    // gateway reports this tier as `seekable: false`.
    //
    // Seeking it anyway is what produced PIPELINE_ERROR_DECODE on the audio
    // packet at exactly the seek target: reported at 0.766259s for a stream
    // whose offset was 18.936 with the viewer at 19.70, and at three other
    // marks on three other videos, each time the number the player had just
    // set currentTime to.
    const back = hidden(videos)
    const seeks: number[] = []
    Object.defineProperty(back, 'currentTime', {
      configurable: true,
      get: () => 0,
      set: (v: number) => seeks.push(v),
    })

    visible(videos).currentTime = 25
    await act(async () => {
      fireEvent.loadedMetadata(back)
    })
    await settle()

    expect(seeks).toEqual([])
  })
})

describe('a tier that breaks after the viewer is already on it', () => {
  it('falls back to the rendition that works instead of stopping', async () => {
    const videos = await mounted()
    await climbToRemux(videos)

    // The mux loses its audio input a second in. The browser reports it as a
    // decode error on the element the viewer is watching — the same shape as
    // any other failure of the source in front of them.
    await act(async () => {
      fireEvent.error(visible(videos))
    })
    await settle()

    // Back to 360p, which was playing perfectly a moment ago and still is.
    await waitFor(() => {
      const front = visible(videos)
      const back = hidden(videos)
      expect(front.src + back.src).toContain(INSTANT_URL)
    })
  })
})

describe('a tier that fails with nothing underneath it', () => {
  it('does not sit silent when the only tier is the one that broke', async () => {
    // Withholding an unverified instant URL is now ordinary, so the muxed stream
    // is often the *opening* tier rather than a climb — observed in the ingest
    // log as `live mux opened ... from=0`. Retreating assumes there is something
    // to retreat to; here there is not, and the retreat swallowed the retry and
    // the failure report with it, leaving a video that never started and never
    // said why.
    remuxOnly = true
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain('/remux'))

    const streamAsks = () =>
      client.getQueryState(['stream', 'abc'])?.dataUpdateCount ?? 0
    const before = streamAsks()

    await act(async () => {
      fireEvent.error(visible(videos))
    })
    await settle()

    // The stream answer is asked for again rather than the player going quiet.
    await waitFor(() => expect(streamAsks()).toBeGreaterThan(before))
  })
})
