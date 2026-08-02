import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'
import { fireIntersection } from '@/test/setup'

/**
 * Scrolling past the player on the watch page, and getting back from it.
 *
 * The reported bug was that the expand button did nothing once the player had
 * folded into the corner: it asked the router for the page it was already on,
 * which is not a navigation, so nothing changed. These tests drive the crossing
 * directly rather than pretending to scroll, because jsdom has no layout and an
 * IntersectionObserver there can only report what a test tells it to.
 *
 * Position is the assertion because it is the one honest signal available: the
 * full-size player is `absolute` in the document, the miniplayer is `fixed` to
 * the viewport, and jsdom preserves inline styles even though it computes no
 * geometry.
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

function renderWatch() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  return render(
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

async function readyPlayer() {
  renderWatch()
  const media = await waitFor(() => {
    const el = document.querySelector('video')
    expect(el).not.toBeNull()
    return el!
  })
  return { media, slot: screen.getByTestId('player-slot') }
}

const host = () => screen.getByTestId('player-host')

/**
 * Lets the bridging frame retire.
 *
 * Changing coordinate space takes two frames on purpose — one that is already in
 * the destination space but shows the old pixels, then the one that animates —
 * and the second is released on a requestAnimationFrame. A synchronous `act`
 * never reaches it, so without this the assertions would be looking at the
 * halfway frame and calling it the result.
 */
const settle = () => act(async () => void (await new Promise((r) => setTimeout(r, 20))))

/** Scrolling the slot out of view is the only way into the miniplayer here. */
async function scrollPastPlayer(slot: Element) {
  act(() => fireIntersection(slot, false))
  await settle()
}

describe('expanding from the miniplayer', () => {
  beforeEach(() => {
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  })

  it('folds into the corner when the slot scrolls out of view', async () => {
    const { slot } = await readyPlayer()
    expect(host().style.position).toBe('absolute')

    await scrollPastPlayer(slot)

    expect(host().style.position).toBe('fixed')
    expect(host().style.width).toBe('400px')
  })

  it('comes back to full size when expanded, without reloading the video', async () => {
    const { media, slot } = await readyPlayer()
    await scrollPastPlayer(slot)

    await act(async () => {
      fireEvent.click(screen.getAllByLabelText('Expand player')[0])
    })

    expect(host().style.position).toBe('absolute')
    // The fix must not be paid for with the invariant the whole feature rests
    // on: it is still the same element, still playing.
    expect(document.querySelector('video')).toBe(media)
    expect(media.isConnected).toBe(true)
  })

  it('scrolls back to the player, since full size is where the slot is', async () => {
    const { slot } = await readyPlayer()
    await scrollPastPlayer(slot)

    await act(async () => {
      fireEvent.click(screen.getAllByLabelText('Expand player')[0])
    })

    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0 })
  })
})

describe('closing the miniplayer while the watch page is open', () => {
  beforeEach(() => {
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  })

  it('returns the player to its slot instead of destroying it', async () => {
    const { media, slot } = await readyPlayer()
    await scrollPastPlayer(slot)

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Close player'))
    })

    // Not a dead black box: the player is still there, back in its slot.
    expect(document.querySelector('video')).toBe(media)
    expect(host().style.position).toBe('absolute')
  })

  it('pauses, because an unseen player that is still audible is the original bug', async () => {
    const pause = vi.spyOn(HTMLMediaElement.prototype, 'pause')
    const { slot } = await readyPlayer()
    await scrollPastPlayer(slot)

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Close player'))
    })

    expect(pause).toHaveBeenCalled()
  })

  it('does not spring back on the next scroll down', async () => {
    const { slot } = await readyPlayer()
    await scrollPastPlayer(slot)

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Close player'))
    })

    await scrollPastPlayer(slot)

    expect(host().style.position).toBe('absolute')
  })

  it('starts working again once the viewer has scrolled back to the player', async () => {
    const { slot } = await readyPlayer()
    await scrollPastPlayer(slot)

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Close player'))
    })

    // Scrolling up to the player is what expires the dismissal.
    act(() => fireIntersection(slot, true))
    await settle()
    await scrollPastPlayer(slot)

    expect(host().style.position).toBe('fixed')
  })
})
