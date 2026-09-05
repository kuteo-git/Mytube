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
/** The ladder this video publishes, so a test can give one that lacks 4K. */
let levelsForThisVideo: Array<{ height: number }> = []

vi.mock('hls.js', () => {
  class FakeHls {
    static isSupported() {
      return true
    }
    static Events = {
      ERROR: 'hlsError',
      MANIFEST_PARSED: 'hlsManifest',
      LEVEL_SWITCHED: 'hlsLevelSwitched',
      SUBTITLE_TRACKS_UPDATED: 'hlsSubtitleTracksUpdated',
    }
    // The caption renditions, which for these videos is none.
    //
    // Present rather than omitted because the player asks the library for them
    // now: a broadcast's captions live inside the master playlist, so selecting
    // one is a call into hls.js rather than a `<track>` element. A mock missing
    // the property does not model a library that always has it, and the four
    // quality tests failed on `undefined.length` before it was added.
    subtitleTracks: Array<{ lang?: string; name?: string }> = []
    subtitleTrack = -1
    subtitleDisplay = false
    // The ladder the server resolved for this video, highest first. Seven rungs,
    // matching ingest's `maxRenditions`, so what is under test is the menu the
    // household actually gets — including the 360 and 240 that exist for ABR and
    // are deliberately not named in it.
    get levels() {
      return levelsForThisVideo
    }
    set currentLevel(v: number) {
      currentLevel = v
    }
    get currentLevel() {
      return currentLevel
    }

    // Handlers, kept rather than dropped: the menu is built from what the
    // library *reports*, so a fake that never reports leaves the player looking
    // at a video with no ladder — a different thing under test.
    private handlers: Record<string, Array<(event: string, data: unknown) => void>> = {}
    on(event: string, cb: (event: string, data: unknown) => void) {
      ;(this.handlers[event] ??= []).push(cb)
    }
    private emit(event: string, data: unknown) {
      for (const cb of this.handlers[event] ?? []) cb(event, data)
    }
    loadSource(url: string) {
      loadSource(url)
      // What the real library does: parse the manifest, announce the ladder,
      // then announce which rung it settled on.
      //
      // 720p whatever the ladder is, found by height rather than by index — a
      // literal index means one number for the seven-rung ladder and another for
      // a video that tops out at 1080p, and a fake that reports a rung which
      // does not exist teaches a test the wrong lesson.
      this.emit(FakeHls.Events.MANIFEST_PARSED, {})
      this.emit(FakeHls.Events.LEVEL_SWITCHED, {
        level: levelsForThisVideo.findIndex((l) => l.height === 720),
      })
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
  // The full ladder unless a test says otherwise, and reset per test so one that
  // narrows it cannot decide the answer for the next.
  levelsForThisVideo = [
    { height: 2160 }, { height: 1440 }, { height: 1080 }, { height: 720 },
    { height: 480 }, { height: 360 }, { height: 240 },
  ]
  window.localStorage.clear()

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
    //
    // Index 2, because 1080p is the third rung of a ladder that now starts at
    // 4K. The number was 0 while the ladder was 1080/720/480 — a test can encode
    // the shape of a ladder without meaning to.
    expect(currentLevel).toBe(2)
    expect(loadSource).toHaveBeenCalledTimes(1)
    expect(destroy).not.toHaveBeenCalled()
  })

  it('names the rungs somebody would choose, and not the ones they would not', async () => {
    await mounted()
    await waitFor(() => expect(loadSource).toHaveBeenCalled())

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })

    const resolution = screen
      .getAllByRole('radiogroup')
      .find((g) => g.getAttribute('aria-label') === 'Resolution')!

    // 4K and 2K rather than 2160p and 1440p, because those are the names people
    // use. And 360/240 are absent although the ladder carries them: they exist
    // so ABR has somewhere to go on a bad minute, and a menu row for one is a
    // row whose only honest use is admitting the connection is bad.
    const labels = within(resolution)
      .getAllByRole('radio')
      .map((b) => b.textContent)
    expect(labels).toEqual(['Auto (720p)', '4K', '2K', '1080p', '720p', '480p'])
  })

  it('keeps a pinned rung on the next video, and says so when that video has none', async () => {
    // A pinned choice is a standing preference: it lives in localStorage and
    // survives from one video to the next, which is the whole reason it is
    // stored. But the ladder belongs to the *video* — most uploads publish no
    // 4K at all — so the question is what a menu should say when the rung
    // somebody pinned does not exist here.
    window.localStorage.setItem('quality', '2160')

    // This video tops out at 1080p, like most of the library.
    levelsForThisVideo = [{ height: 1080 }, { height: 720 }, { height: 480 }]

    await mounted()
    await waitFor(() => expect(loadSource).toHaveBeenCalled())

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })

    const resolution = screen
      .getAllByRole('radiogroup')
      .find((g) => g.getAttribute('aria-label') === 'Resolution')!
    const checked = within(resolution)
      .getAllByRole('radio')
      .filter((b) => b.getAttribute('aria-checked') === 'true')
      .map((b) => b.textContent)

    // hls.js was told 2160, found no such rung and went to automatic — the right
    // thing, and documented on selectHeight. The menu has to say so: it drew
    // `value={2160}` against a list with no 2160 in it, so nothing was
    // highlighted at all and the viewer saw a row of unselected buttons while
    // the player was quietly on automatic.
    expect(checked).toEqual(['Auto (720p)'])

    // And the pin survives. It is a standing preference; a single 1080p video
    // must not be able to discard it.
    expect(window.localStorage.getItem('quality')).toBe('2160')
  })

  it('says which rung Auto settled on', async () => {
    await mounted()
    await waitFor(() => expect(loadSource).toHaveBeenCalled())

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })

    // The fake settled on level 3, which is 720p. Without this a viewer looking
    // at a soft picture cannot tell a ladder that dropped for a slow minute from
    // a video that is simply broken — both are "Auto" over a blurry frame.
    expect(screen.getByText('Auto (720p)')).toBeTruthy()
  })
})
