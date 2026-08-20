import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router-dom'
import { pageRoutes } from '@/app/routes'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
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
  hls: { url: '/api/videos/abc/hls/master.m3u8', height: 360, name: 'hls' },
  sources: [{ name: 'hls', url: '/api/videos/abc/hls/master.m3u8', height: 360, seekable: true }],
}

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getVideo: vi.fn(async () => video),
    getVideoEnsuring: vi.fn(async () => video),
    getStream: vi.fn(async () => stream),
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
    // The real pages render underneath now, so whatever they ask for has to
    // answer. Empty is the honest answer for all of it.
    listHistory: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    listPinned: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    listTopPlayed: vi.fn(async () => []),
    listScans: vi.fn(async () => ({ scans: [], total: 0 })),
    getScanStatus: vi.fn(async () => ({ running: false, sources: [] })),
    discover: vi.fn(async () => []),
    search: vi.fn(async () => ({ library: [], youtube: [] })),
    getChannel: vi.fn(async () => null),
    listChannelVideos: vi.fn(async () => ({ videos: [], nextPageToken: '', sortOptions: [] })),
  },
}))

const settle = () => act(async () => void (await new Promise((r) => setTimeout(r, 20))))

/**
 * Reads the router, and drives it.
 *
 * The shell renders the app's own route table now — one table, so that the page
 * held underneath the watch layer cannot drift from the page a link goes to —
 * which means a test cannot swap in stand-in pages. It says where it is and
 * moves itself instead.
 */
let go: (to: string) => void = () => {}
function Probe() {
  const location = useLocation()
  go = useNavigate()
  return <span data-testid="path">{location.pathname}</span>
}
const path = () => screen.getByTestId('path').textContent

/**
 * The shell, mounted the way main.tsx mounts it.
 *
 * Tabs have to be *arrived at* rather than listed as history: the shell
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
        <Probe />
        <Routes>
          <Route element={<AppShell />}>{pageRoutes}</Route>
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
  act(() => go('/watch/abc'))
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

  it('holds on to the very same page, not a rebuilt copy of it', async () => {
    // The fault this was reported for. Swapping `<Outlet/>` for `<Routes>` when
    // the layer opens looks like keeping the page, and is not: React reconciles
    // by element type at each position, so the two are a teardown and a fresh
    // build. Same pixels, no state — everything scrolled, expanded or loaded on
    // the tab was lost the moment a video was opened, which rather defeats
    // keeping it there.
    //
    // Node identity is the strongest form of the check available here: a
    // rebuilt tree cannot hand back the node it replaced.
    phone(['/history'])
    await settle()
    const before = document.querySelector('main')?.firstElementChild
    expect(before).toBeTruthy()

    act(() => go('/watch/abc'))
    await waitFor(() => expect(document.querySelector('video')).not.toBeNull())
    await settle()

    expect(document.querySelector('main')?.firstElementChild).toBe(before)
    expect(document.contains(before!)).toBe(true)
  })

  it('leaves the page underneath at the offset it was scrolled to', async () => {
    // Reported three times. jsdom computes no layout, so scrollTop is backed by
    // a plain number here — which is all anything in this path reads or writes.
    phone(['/history'])
    await settle()

    const main = document.querySelector('main')!
    let top = 0
    Object.defineProperty(main, 'scrollTop', {
      get: () => top,
      set: (v: number) => {
        top = v
      },
      configurable: true,
    })
    Object.defineProperty(main, 'scrollHeight', { value: 5000, configurable: true })
    Object.defineProperty(main, 'clientHeight', { value: 800, configurable: true })

    act(() => {
      top = 900
      main.dispatchEvent(new Event('scroll'))
    })

    act(() => go('/watch/abc'))
    await waitFor(() => expect(document.querySelector('video')).not.toBeNull())
    await settle()

    // Opening the video must not move the page it was opened from: it is still
    // there, and the drag is about to reveal it.
    expect(main.scrollTop).toBe(900)

    act(() => go(-1 as unknown as string))
    await settle()

    // And coming back must not move it either. Nothing scrolled in between.
    expect(main.scrollTop).toBe(900)
  })

  it('does not rewind the tab when the bar is expanded back to the video', async () => {
    // Found by reading rather than by reasoning, after the offset went on being
    // lost with the obvious paths ruled out. Expanding scrolls the page to the
    // top so the desktop player's slot is on screen — but on a phone there is
    // no slot, the player is `fixed`, and the only thing that scroll reaches is
    // the tab underneath. Tab, bar, expand, drag: the tab came back at the top.
    phone(['/history'])
    await settle()

    const main = document.querySelector('main')!
    let top = 0
    Object.defineProperty(main, 'scrollTop', {
      get: () => top,
      set: (v: number) => {
        top = v
      },
      configurable: true,
    })
    main.scrollTo = ((o: ScrollToOptions) => {
      top = o.top ?? 0
    }) as HTMLElement['scrollTo']

    act(() => go('/watch/abc'))
    await waitFor(() => expect(document.querySelector('video')).not.toBeNull())
    await settle()
    act(() => go(-1 as unknown as string))
    await settle()

    top = 900
    act(() => {
      fireEvent.click(screen.getAllByLabelText('Expand player')[0])
    })
    await settle()

    expect(main.scrollTop).toBe(900)
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

  it('hides the header and the bottom bar, without removing them', async () => {
    // A screen of its own rather than a page inside the app's chrome — but both
    // bars stay in the tree at zero opacity, because they belong to the page
    // underneath and have to arrive *with* it. Omitted, they appeared only once
    // the navigation committed: the bar arrived after its page, and the
    // scroller's top padding came with it, so the content jumped 56px at the
    // very end of the drag.
    await openVideoFrom('/history')

    // Queried through the DOM, not by role: `aria-hidden` is doing its job, so
    // neither bar is in the accessibility tree to be found by one. That is the
    // point — a bar on its way in should not be announced as available.
    const header = document.querySelector('header')!
    const nav = document.querySelector('nav[aria-label="Main"]') as HTMLElement
    expect(header.style.opacity).toBe('0')
    expect(nav.style.opacity).toBe('0')
    expect(header.getAttribute('aria-hidden')).toBe('true')
    expect(nav.getAttribute('aria-hidden')).toBe('true')
  })

  it('reserves the header\'s room throughout, so nothing jumps at the end', async () => {
    // `--top-bar` rather than a literal: the bar's own height plus the status
    // bar it bleeds up under, added in one place so the half-dozen things that
    // begin beneath it cannot disagree about where that is.
    await openVideoFrom('/history')
    expect(document.querySelector('main')?.className).toContain('pt-[var(--top-bar)]')
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

    expect(path()).toBe('/history')
  })

  it('drops the same two bars on a channel, without making it a layer', async () => {
    // A channel opened on a phone is a screen of its own too — its own subject,
    // its own way back, which ChannelPage draws. But nothing underneath has to
    // stay alive, so it is an ordinary page: `data-background` stays absent.
    phone(['/subscriptions'])
    await settle()
    act(() => go('/channel/c1'))
    await settle()

    expect(document.querySelector('main')?.getAttribute('data-background')).toBeNull()
    expect(document.querySelector('header')?.style.opacity).toBe('0')
    expect(
      (document.querySelector('nav[aria-label="Main"]') as HTMLElement | null)?.style.opacity,
    ).toBe('0')
  })

  it('gives every screen you arrive at a back bar and a title', async () => {
    // Storage, Activity, Saved and each Settings panel are all somewhere you go
    // on purpose, so each is a screen of its own rather than a page inside the
    // app's chrome — the same treatment as a channel.
    phone(['/settings'])
    await settle()
    act(() => go('/settings/narration'))
    await settle()

    expect(screen.getByRole('button', { name: 'Back' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Narration' })).toBeInTheDocument()
    expect(
      (document.querySelector('nav[aria-label="Main"]') as HTMLElement | null)?.style.opacity,
    ).toBe('0')
  })

  it('leaves Settings itself as a tab, bars and all', async () => {
    // A prefix match on `/settings` would have taken the tab bar away from the
    // tab that leads to all of them.
    phone(['/settings'])
    await settle()

    expect(screen.queryByRole('button', { name: 'Back' })).not.toBeInTheDocument()
    expect(
      (document.querySelector('nav[aria-label="Main"]') as HTMLElement | null)?.style.opacity,
    ).toBe('1')
  })

  it('keeps the app\'s chrome on a channel opened on a desktop', async () => {
    desktop(['/subscriptions'])
    await settle()
    act(() => go('/channel/c1'))
    await settle()

    expect(screen.getByLabelText('Toggle sidebar')).toBeInTheDocument()
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
