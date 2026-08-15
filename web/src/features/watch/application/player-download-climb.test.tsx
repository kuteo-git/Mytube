import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'

/**
 * Climbing to the local file when the download lands mid-playback.
 *
 * This is the tier machinery's last step and the one nothing tested. The
 * reported fault was a video that opened at 360p, finished downloading, and
 * stayed at 360p — until 1080p was pressed by hand or the page was reloaded,
 * both of which work by starting the tier machinery over rather than by fixing
 * anything.
 *
 * Two things had to be true at once for that to happen, and both are here:
 *
 *  - a stale `error` from the muxed stream — a 502 from the gateway, which the
 *    ingest log carried for every video — abandoned whichever climb was
 *    current, without checking that the climb was the one that failed;
 *  - and once the local file is in the stream answer, `useStream` stops
 *    polling. That poll was the only thing re-running the climb effect, so an
 *    abandoned climb to the local file was never attempted again.
 *
 * The second is what turned a lost climb into a permanent one, so it is the
 * one worth holding onto: whatever loses a climb, the player must try again.
 */

const REMUX_URL = '/api/videos/abc/remux'
const INSTANT_URL = 'blob:instant'
const LOCAL_URL = '/media/abc/1080p.mp4'

/** Flipped by a test the moment the download is meant to have finished. */
let localReady = false

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
  title: 'A long video',
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
    // The answer a video still downloading gives, until it is not.
    getStream: vi.fn(async () =>
      localReady
        ? { local: { url: LOCAL_URL, height: 1080, name: 'local' }, instant: null, remux: null }
        : {
            local: null,
            instant: { url: INSTANT_URL, height: 360, name: 'instant' },
            remux: { url: REMUX_URL, height: 1080, name: 'remux' },
          },
    ),
    getRemuxStart: vi.fn(async (_id: string, at: number) => at - 2.028),
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
  localReady = false
  window.localStorage.clear()
})

function renderWatch() {
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
}

/** jsdom has no media pipeline: nothing the handover reads moves on its own. */
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
  renderWatch()
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

/**
 * The download finishing, as the player learns of it: the next poll of the
 * stream answer comes back with a local file and nothing else.
 */
async function downloadLands() {
  localReady = true
  await act(async () => {
    await client.invalidateQueries({ queryKey: ['stream', 'abc'] })
  })
  await settle()
}

describe('the download landing while the video is being watched', () => {
  it('climbs from the low rendition to the local file', async () => {
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain(INSTANT_URL))
    visible(videos).currentTime = 30

    await downloadLands()

    await waitFor(() => expect(hidden(videos).src).toContain(LOCAL_URL))
    // Parked a moment ahead of the playhead, then handed over when the playhead
    // arrives — which in jsdom means saying so.
    await act(async () => {
      fireEvent.loadedMetadata(hidden(videos))
    })
    visible(videos).currentTime = 40
    await act(async () => {
      fireEvent.seeked(hidden(videos))
    })
    await waitFor(() => expect(visible(videos).src).toContain(LOCAL_URL))
  })

  it('tries again when the climb to the local file is lost', async () => {
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain(INSTANT_URL))
    visible(videos).currentTime = 30

    await downloadLands()
    await waitFor(() => expect(hidden(videos).src).toContain(LOCAL_URL))

    // The layer being prepared fails. Nothing polls any more — the stream
    // answer has a local file in it, which is where `useStream` stops — so
    // unless abandoning a climb asks for another one, this is the end of it and
    // the viewer stays on 360p for the rest of the video.
    await act(async () => {
      fireEvent.error(hidden(videos))
    })
    await settle()

    await waitFor(() => expect(hidden(videos).src).toContain(LOCAL_URL))
  })

  it('ignores a failure belonging to a stream it has already moved on from', async () => {
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain(INSTANT_URL))
    // The muxed stream is being prepared: this is the claim about to be
    // replaced, and the 502 about to arrive is its.
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
    visible(videos).currentTime = 30

    // The gateway answers the mux with a 502 — every video in the ingest log
    // did — and the error lands after the local file has taken the claim.
    localReady = true
    await act(async () => {
      const stale = hidden(videos)
      const climb = client.invalidateQueries({ queryKey: ['stream', 'abc'] })
      fireEvent.error(stale)
      await climb
    })
    await settle()

    // The climb the error had nothing to do with is still standing.
    await waitFor(() => expect(hidden(videos).src).toContain(LOCAL_URL))
  })
})
