import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'

/**
 * The HLS tier on a browser that needs hls.js — which is to say, on a desktop.
 *
 * jsdom has no `MediaSource` and no `ManagedMediaSource`, so every other player
 * test here runs as a browser that can play neither and quietly falls back to
 * the muxed stream. That is useful — it proves the fallback — and it means
 * nothing in the suite exercised this path until now.
 *
 * Two things have to be right, and both fail silently:
 *
 * 1. The playlist must NOT be assigned to `src`. On a browser with no native
 *    HLS that is an immediate `MEDIA_ERR_SRC_NOT_SUPPORTED`, measured on Chrome
 *    2026-08-20 — and the player would count a dead tier before the library had
 *    a chance to attach.
 * 2. The element must still be identifiable as playing that source. hls.js
 *    replaces `src` with a `blob:` of its own, so the three identity checks in
 *    the tier machinery — the handover, the claim, the failure — would all say
 *    "not mine", and a climb would silently never complete.
 */

const HLS_URL = '/api/videos/abc/hls/master.m3u8'
const REMUX_URL = '/api/videos/abc/remux'

const loadSource = vi.fn()
const attachMedia = vi.fn()
const destroy = vi.fn()
/** The rung hls.js was told to play; -1 is automatic. */
let currentLevel = -1

vi.mock('hls.js', () => {
  class FakeHls {
    static isSupported() {
      return true
    }
    static Events = { ERROR: 'hlsError', MANIFEST_PARSED: 'hlsManifest' }
    // The ladder the server resolved for this video, highest first.
    levels = [{ height: 1080 }, { height: 720 }, { height: 480 }]
    set currentLevel(v: number) {
      currentLevel = v
    }
    get currentLevel() {
      return currentLevel
    }
    on() {}
    loadSource(url: string) {
      loadSource(url)
    }
    attachMedia(el: HTMLVideoElement) {
      attachMedia(el)
      // What the real library does, and the whole reason `data-source` exists.
      Object.defineProperty(el, 'src', { configurable: true, value: 'blob:fake-hls' })
    }
    destroy() {
      destroy()
    }
  }
  return { default: FakeHls }
})

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
  title: 'A video still downloading',
  channel,
  durationSeconds: 640,
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
      instant: null,
      // Both offered, as the gateway does: what can play which differs by
      // browser and the server does not guess.
      hls: { url: HLS_URL, height: 720, name: 'hls' },
      remux: { url: REMUX_URL, height: 720, name: 'remux' },
    })),
    getRemuxStart: vi.fn(async () => 0),
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

const settle = (ms = 30) => act(async () => void (await new Promise((r) => setTimeout(r, ms))))

beforeEach(() => {
  // A desktop: MediaSource and no native HLS. Chrome, in other words.
  //
  // The vendor has to be set explicitly. jsdom reports
  // `navigator.vendor === "Apple Computer, Inc."` out of the box, which the
  // capability check reads — correctly — as a Safari, so without this the test
  // runs as a browser with native HLS and never reaches hls.js at all.
  Object.defineProperty(window.navigator, 'vendor', {
    configurable: true,
    get: () => 'Google Inc.',
  })
  Object.defineProperty(window.navigator, 'userAgent', {
    configurable: true,
    get: () =>
      'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36',
  })
  ;(window as unknown as Record<string, unknown>).MediaSource = function () {}
  currentLevel = -1
  loadSource.mockClear()
  attachMedia.mockClear()
  destroy.mockClear()
  window.localStorage.clear()
})

afterEach(() => {
  delete (window as unknown as Record<string, unknown>).MediaSource
})

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
  await settle()
  return videos
}

function visible(videos: NodeListOf<HTMLVideoElement>) {
  return Array.from(videos).find((v) => v.getAttribute('aria-hidden') !== 'true') ?? videos[0]
}

describe('the HLS tier where the browser needs hls.js', () => {
  it('opens on the playlist rather than the muxed stream', async () => {
    await mounted()

    await waitFor(() => expect(loadSource).toHaveBeenCalledWith(HLS_URL))
    expect(attachMedia).toHaveBeenCalled()
  })

  it('does not assign the playlist to src, which would fail before the library loads', async () => {
    const videos = await mounted()
    await waitFor(() => expect(loadSource).toHaveBeenCalled())

    // Whatever `src` holds, it is never the playlist: assigning that on a
    // browser without native HLS is MEDIA_ERR_SRC_NOT_SUPPORTED, immediately.
    for (const el of videos) {
      expect(el.getAttribute('src')).not.toBe(HLS_URL)
    }
  })

  it('keeps the layer identifiable after hls.js replaces src with a blob', async () => {
    const videos = await mounted()
    await waitFor(() => expect(attachMedia).toHaveBeenCalled())

    const front = visible(videos)
    // The library has taken `src` over…
    expect(front.src).toBe('blob:fake-hls')
    // …and the machinery can still tell what this layer is playing. Without
    // this every claim comparison says "not mine" and climbs stop completing.
    expect(front.dataset.source).toBe(HLS_URL)
  })

  it('tears the attachment down when the player goes away', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
    const { unmount } = render(
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
    await waitFor(() => expect(attachMedia).toHaveBeenCalled())

    unmount()

    // Otherwise a page-away leaves a worker fetching segments for a video
    // nobody is watching.
    await waitFor(() => expect(destroy).toHaveBeenCalled())
  })
})

describe('the quality control where a rendition can actually be chosen', () => {
  it('is offered, and moves the ladder rather than reloading the video', async () => {
    await mounted()
    await waitFor(() => expect(loadSource).toHaveBeenCalled())

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })

    const resolution = screen
      .getAllByRole('radiogroup')
      .find((g) => g.getAttribute('aria-label') === 'Resolution')
    expect(resolution).toBeTruthy()

    await act(async () => {
      fireEvent.click(within(resolution!).getByText('1080p'))
    })
    await settle()

    // The rung changed on the running attachment. Nothing was re-attached and
    // no source was reloaded: hls.js switches at the next segment boundary, so
    // the picture does not restart to change quality.
    expect(currentLevel).toBe(0)
    expect(loadSource).toHaveBeenCalledTimes(1)
    expect(destroy).not.toHaveBeenCalled()
  })
})
