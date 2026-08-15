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
import { describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'

/**
 * The one thing about the miniplayer a machine can check here, and the thing
 * that was actually broken.
 *
 * The old implementation portal'd the Player between two containers. Changing a
 * portal's container does not move the DOM: React tears the subtree down and
 * builds a new one, `<video>` included, so the element that was playing is
 * discarded and a fresh one starts from nothing. On screen that read as sound
 * with no picture, and no amount of animation work could have addressed it.
 *
 * The assertion is therefore about *node identity*, not about anything merely
 * being present. A test that only checked "a video exists" would have passed
 * against the broken version, which is exactly how that version came to be
 * handed over as working.
 *
 * Navigation happens through the router already on screen. Re-rendering a fresh
 * MemoryRouter would remount the whole tree and report a new node no matter
 * what the code did — the test would fail identically whether or not the bug
 * was present, and so would be measuring nothing.
 */

const stream = {
  local: null,
  instant: { url: 'blob:instant', height: 360, name: 'instant' },
  remux: null,
  sources: [{ name: 'instant', url: 'blob:instant', height: 360, seekable: true }],
}

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
    listHistory: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    listPinned: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    listTopPlayed: vi.fn(async () => []),
    discover: vi.fn(async () => []),
  },
}))

let go: (to: string) => void = () => {}

function Navigator() {
  go = useNavigate()
  return null
}

function renderApp() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/watch/abc']}>
        <Navigator />
        <Probe />
        <Routes>
          <Route element={<AppShell />}>{pageRoutes}</Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

/**
 * Says where the router is.
 *
 * The shell renders the app's own route table now — one table, so the page held
 * underneath the watch layer cannot drift from the page a link goes to — which
 * means a stand-in page can no longer stand in for the real one.
 */
function Probe() {
  return <span data-testid="path">{useLocation().pathname}</span>
}
const atHome = () => screen.findByText('/', { selector: '[data-testid="path"]' })

describe('player continuity', () => {
  it('keeps the very same <video> node when leaving the watch page', async () => {
    renderApp()

    const before = await waitFor(() => {
      const el = document.querySelector('video')
      expect(el).not.toBeNull()
      return el!
    })

    await act(async () => {
      go('/')
    })

    await atHome()

    expect(document.querySelector('video')).toBe(before)
    expect(before.isConnected).toBe(true)
  })

  it('still holds the same node on the way back to the watch page', async () => {
    renderApp()

    const before = await waitFor(() => {
      const el = document.querySelector('video')
      expect(el).not.toBeNull()
      return el!
    })

    await act(async () => {
      go('/')
    })
    await atHome()

    await act(async () => {
      go('/watch/abc')
    })

    await waitFor(() => expect(document.querySelector('video')).toBe(before))
    expect(before.isConnected).toBe(true)
  })
})

/**
 * Reloading the page while watching, then leaving it.
 *
 * A reload on the watch page is not a fresh start: `last-watched` is written
 * every fifteen seconds, so the entry describing the video now on screen is
 * already in storage when the page comes up. The resume offer reads that entry
 * once, at mount, and waits for the player to be free before putting it in the
 * corner — and on the watch page it is not free, so it waits.
 *
 * The mistake is what "free" meant. It was read as "no player state", and there
 * is an instant during the walk back to the home page where that is true of a
 * player which is about to be a miniplayer. So the offer fired into that gap and
 * put a second window in the corner beside the one already arriving — a corner
 * with two videos in it, the second only visible once the first was closed.
 */
describe('reloading on the watch page and then leaving it', () => {
  it('leaves one player in the corner, not two', async () => {
    window.localStorage.setItem(
      'yt-last-watched',
      JSON.stringify({ videoId: 'abc', positionSeconds: 30, savedAt: Date.now() }),
    )
    try {
      renderApp()
      await waitFor(() => expect(document.querySelector('video')).not.toBeNull())

      await act(async () => {
        go('/')
      })
      await atHome()
      // Long enough for the resume offer's own fetch to land and its effect to
      // run: the second window arrived a moment after the first, which is why
      // this was only ever seen by hand.
      await act(async () => {
        await new Promise((r) => setTimeout(r, 100))
      })

      expect(screen.getAllByTestId('player-host')).toHaveLength(1)

      // Closing it is an answer, and the answer must hold. The offer had only
      // ever counted itself as spent when it was the thing that made the
      // offer — so a player put in the corner by the watch page left it armed,
      // waiting for exactly the state that closing produces. Press the close
      // button and the same video came straight back.
      await act(async () => {
        fireEvent.click(screen.getByLabelText('Close player'))
      })
      await act(async () => {
        await new Promise((r) => setTimeout(r, 100))
      })

      expect(screen.queryByTestId('player-host')).not.toBeInTheDocument()
    } finally {
      window.localStorage.removeItem('yt-last-watched')
    }
  })
})
