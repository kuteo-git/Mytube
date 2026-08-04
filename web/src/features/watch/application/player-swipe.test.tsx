import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'
import { MOBILE_BREAKPOINT } from './player-geometry'

// Copied from player-expand.test.tsx rather than trimmed: the page reads more
// of this than it looks, and a missing field surfaces as an unrelated crash.
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
  title: 'A video',
  channel,
  durationSeconds: 240,
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

const stream = {
  local: null,
  instant: { url: 'blob:instant', height: 360, name: 'instant' },
  remux: null,
  sources: [{ name: 'instant', url: 'blob:instant', height: 360, seekable: true }],
}

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getVideo: vi.fn(async () => video),
    getVideoEnsuring: vi.fn(async () => video),
    getStream: vi.fn(async () => stream),
    listUpNext: vi.fn(async () => []),
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

/** The player's own surface — the element the finger lands on. */
const surface = () => screen.getByTestId('player-host').querySelector('.group\\/player')!

const settle = () => act(async () => void (await new Promise((r) => setTimeout(r, 20))))

async function renderOnAPhone() {
  Object.defineProperty(window, 'innerWidth', {
    value: MOBILE_BREAKPOINT - 40,
    configurable: true,
  })
  Object.defineProperty(window, 'innerHeight', { value: 800, configurable: true })

  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/watch/abc']}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<h1>home</h1>} />
            <Route path="/watch/:videoId" element={<WatchPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  await waitFor(() => expect(document.querySelector('video')).not.toBeNull())
  await settle()

  // jsdom has no layout, so the height the gesture measures itself against has
  // to be supplied. 220px is about what a 16:9 band is on a phone this wide.
  const el = surface()
  el.getBoundingClientRect = () =>
    ({ top: 56, left: 0, width: MOBILE_BREAKPOINT - 40, height: 220 }) as DOMRect
  return el
}

/**
 * One finger, from a point, through some points, to a release.
 *
 * The clock is driven rather than waited on. `event.timeStamp` cannot be set
 * from a test — it is read-only and jsdom leaves it at zero — which is why the
 * gesture reads `performance.now()`, and why that is what a test moves.
 */
function drag(
  el: Element,
  { from, to, ms = 300 }: { from: [number, number]; to: [number, number]; ms?: number },
) {
  const opts = { pointerId: 1, pointerType: 'touch', isPrimary: true }
  let clock = 0
  vi.spyOn(performance, 'now').mockImplementation(() => clock)
  act(() => {
    fireEvent.pointerDown(el, { ...opts, clientX: from[0], clientY: from[1] })
    // Two moves, because the gesture measures its closing speed across the last
    // segment rather than over the whole journey.
    clock = ms / 2
    fireEvent.pointerMove(el, {
      ...opts,
      clientX: (from[0] + to[0]) / 2,
      clientY: (from[1] + to[1]) / 2,
    })
    clock = ms
    fireEvent.pointerMove(el, { ...opts, clientX: to[0], clientY: to[1] })
    fireEvent.pointerUp(el, { ...opts, clientX: to[0], clientY: to[1] })
  })
}

beforeEach(() => {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('coarse'),
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }))
})

describe('dragging the player down on a phone', () => {
  it('goes back to browsing', async () => {
    // The whole point, and what the removed 2026-08-03 version lacked: pulling
    // a player down is a request to go and look at something else. Minimising
    // in place left it in the corner of the page it was already on.
    const el = await renderOnAPhone()

    drag(el, { from: [200, 100], to: [200, 260] })
    await settle()

    expect(screen.getByRole('heading', { name: 'home' })).toBeInTheDocument()
  })

  it('leaves the player alive rather than closing it', async () => {
    // Going back to browsing is not stopping the video — that is what the close
    // button is for, and the two must not be the same gesture.
    const el = await renderOnAPhone()
    const media = document.querySelector('video')

    drag(el, { from: [200, 100], to: [200, 260] })
    await settle()

    expect(document.querySelector('video')).toBe(media)
  })

  it('springs back from a short, slow drag', async () => {
    const el = await renderOnAPhone()

    drag(el, { from: [200, 100], to: [200, 130], ms: 900 })
    await settle()

    // Still on the watch page, and nothing left half-way to the corner.
    expect(screen.queryByRole('heading', { name: 'home' })).not.toBeInTheDocument()
  })

  it('answers a flick that never travelled far', async () => {
    // A flick is an unambiguous statement; making it travel a quarter of the
    // player first reads as the gesture having been missed.
    const el = await renderOnAPhone()

    drag(el, { from: [200, 100], to: [200, 145], ms: 40 })
    await settle()

    expect(screen.getByRole('heading', { name: 'home' })).toBeInTheDocument()
  })

  it('ignores a tap, which belongs to the controls', async () => {
    // On touch a tap is what shows and hides the chrome. A finger never lands
    // perfectly still, so without a slop every tap would nudge the player.
    const el = await renderOnAPhone()

    drag(el, { from: [200, 100], to: [202, 104] })
    await settle()

    expect(screen.queryByRole('heading', { name: 'home' })).not.toBeInTheDocument()
  })

  it('ignores a sideways swipe', async () => {
    // Sideways over a player belongs to seeking and to the browser's own back
    // gesture. Taking an ambiguous swipe from either is the worse mistake.
    const el = await renderOnAPhone()

    drag(el, { from: [200, 100], to: [40, 190] })
    await settle()

    expect(screen.queryByRole('heading', { name: 'home' })).not.toBeInTheDocument()
  })

  it('ignores an upward drag', async () => {
    const el = await renderOnAPhone()

    drag(el, { from: [200, 300], to: [200, 60] })
    await settle()

    expect(screen.queryByRole('heading', { name: 'home' })).not.toBeInTheDocument()
  })

  it('ignores a mouse, which has a back button', async () => {
    // A drag with a mouse would be a second way to do something that already
    // has an obvious one, and it would take click-to-pause with it.
    const el = await renderOnAPhone()
    const opts = { pointerId: 2, pointerType: 'mouse', isPrimary: true }
    act(() => {
      fireEvent.pointerDown(el, { ...opts, clientX: 200, clientY: 100, timeStamp: 0 })
      fireEvent.pointerMove(el, { ...opts, clientX: 200, clientY: 260, timeStamp: 300 })
      fireEvent.pointerUp(el, { ...opts, clientX: 200, clientY: 260, timeStamp: 300 })
    })
    await settle()

    expect(screen.queryByRole('heading', { name: 'home' })).not.toBeInTheDocument()
  })
})
