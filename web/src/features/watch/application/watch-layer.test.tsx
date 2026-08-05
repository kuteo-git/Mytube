import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { Link, MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'
import { MOBILE_BREAKPOINT } from './player-geometry'

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

const settle = () => act(async () => void (await new Promise((r) => setTimeout(r, 20))))

/** A stand-in tab with a way to open a video from it. */
function Tab({ name }: { name: string }) {
  return (
    <div>
      <h1>{name}</h1>
      <Link to="/watch/abc">open video</Link>
    </div>
  )
}

/**
 * The shell with stand-in tabs.
 *
 * The tabs have to be *arrived at* rather than listed as history: the shell
 * remembers the page it was showing, so an entry it never rendered is an entry
 * it cannot have seen. That is not a limitation of the test — it is exactly
 * what a reload on a watch URL looks like, and why Home stands in for it.
 */
function renderShell(width: number, entries: string[]) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true })
  Object.defineProperty(window, 'innerHeight', { value: 800, configurable: true })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={entries}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<Tab name="home" />} />
            <Route path="/history" element={<Tab name="history" />} />
            <Route path="/saved" element={<Tab name="saved" />} />
            <Route path="/watch/:videoId" element={<WatchPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const phone = (entries: string[]) => renderShell(MOBILE_BREAKPOINT - 40, entries)
const desktop = (entries: string[]) => renderShell(1440, entries)

/** Start on a tab, then open a video from it, the way a viewer does. */
async function openVideoFrom(tab: string) {
  phone([tab])
  await settle()
  act(() => {
    fireEvent.click(screen.getByRole('link', { name: 'open video' }))
  })
  await waitFor(() => expect(document.querySelector('video')).not.toBeNull())
  await settle()
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

describe('the watch screen as a layer', () => {
  /**
   * Which page is being held underneath.
   *
   * Read from the shell rather than from what happens to be rendered, because
   * the layer underneath is driven by the app's own route table — the whole
   * point of it being one table — so a test cannot swap in a stand-in page and
   * still be testing the thing that ships.
   */
  const underneath = () =>
    document.querySelector('main')?.getAttribute('data-background')

  it('keeps the page it was opened from alive underneath', async () => {
    // The reason for the two-layer arrangement: a drag reveals what you came
    // from, and there is nothing to reveal if the router has thrown it away.
    await openVideoFrom('/history')

    expect(underneath()).toBe('/history')
  })

  it('shows the tab you actually came from, not always Home', async () => {
    await openVideoFrom('/saved')

    expect(underneath()).toBe('/saved')
  })

  it('falls back to Home for a link opened cold', async () => {
    // A shared LAN link opens straight onto a video (CLAUDE.md §5). Native apps
    // answer a deep link by synthesising a stack back to the root, and so does
    // this — the gesture then behaves as it does everywhere else.
    phone(['/watch/abc'])
    await settle()

    expect(underneath()).toBe('/')
  })

  it('drops the header and the bottom bar', async () => {
    // A screen of its own rather than a page inside the app's chrome.
    await openVideoFrom('/history')

    expect(screen.queryByLabelText('Toggle sidebar')).not.toBeInTheDocument()
    const nav = screen.queryByRole('navigation', { name: 'Main' })
    // Rendered but invisible: it belongs to the page underneath and arrives
    // with it as the layer is dragged away.
    expect(nav === null || nav.style.opacity === '0').toBe(true)
  })

  it('returns to the tab it came from when dragged away', async () => {
    // Not to Home. This is the whole reason the layer exists rather than a
    // plain navigation: History opened the video, so History is what a drag
    // puts back — and popping the entry is also what returns that page to the
    // scroll position it was left at.
    await openVideoFrom('/history')

    const surface = screen
      .getByTestId('player-host')
      .querySelector('.group\\/player')!
    surface.getBoundingClientRect = () =>
      ({ top: 0, left: 0, width: MOBILE_BREAKPOINT - 40, height: 220 }) as DOMRect

    const opts = { pointerId: 1, pointerType: 'touch', isPrimary: true }
    let clock = 0
    vi.spyOn(performance, 'now').mockImplementation(() => clock)
    act(() => {
      fireEvent.pointerDown(surface, { ...opts, clientX: 200, clientY: 100 })
      clock = 150
      fireEvent.pointerMove(surface, { ...opts, clientX: 200, clientY: 180 })
      clock = 300
      fireEvent.pointerMove(surface, { ...opts, clientX: 200, clientY: 260 })
      fireEvent.pointerUp(surface, { ...opts, clientX: 200, clientY: 260 })
    })
    await settle()

    expect(screen.getByRole('heading', { name: 'history' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'home' })).not.toBeInTheDocument()
  })

  it('is not a layer on a desktop, where the watch page is an ordinary page', async () => {
    // The slot, the drawer and the observer that folds the player into the
    // corner all belong to that arrangement, and none of it needs disturbing.
    desktop(['/history', '/watch/abc'])
    await settle()

    expect(document.querySelector('main')?.getAttribute('data-background')).toBeNull()
    expect(screen.getByLabelText('Toggle sidebar')).toBeInTheDocument()
  })
})
