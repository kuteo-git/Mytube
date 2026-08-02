import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'
import { miniRectDesktop } from '@/features/watch/application/player-geometry'
import { fireIntersection } from '@/test/setup'

/**
 * Which controls each shape of the player offers, and which layer is allowed to
 * say whether it is playing.
 *
 * The second of those is the interesting one. The player keeps two <video>
 * elements permanently mounted and swaps which is in front, and the play/pause
 * state used to be reported only by whichever element was at the front *when
 * React last rendered*. The handover calls play() on the element that is about
 * to come forward while React still has it as the back one, so that event was
 * dropped and the state kept insisting playback had stopped — a play button over
 * a playing video, and controls that could never hide, since they are pinned
 * open whenever nothing is playing.
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
  // Captions have to exist for the caption control to exist at all: a menu over
  // a video with no tracks would be a control that cannot do anything.
  subtitles: [{ language: 'en', label: 'English', url: '/media/abc/en.vtt', generated: false }],
  userState: {
    watchProgress: 0,
    watchPositionSeconds: 0,
    reaction: 'NONE' as const,
    inWatchLater: false,
  },
}

/** Flipped by the handover test to make the download appear to finish. */
let localReady = false

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getVideo: vi.fn(async () => video),
    getVideoEnsuring: vi.fn(async () => video),
    getStream: vi.fn(async () =>
      localReady
        ? {
            ...stream,
            local: { url: '/media/abc/1080p.mp4', height: 1080, name: 'local' },
            sources: [
              ...stream.sources,
              { name: 'local', url: '/media/abc/1080p.mp4', height: 1080, seekable: true },
            ],
          }
        : stream,
    ),
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

const settle = () => act(async () => void (await new Promise((r) => setTimeout(r, 20))))

beforeEach(() => {
  localReady = false
})

async function ready() {
  renderWatch()
  const videos = await waitFor(() => {
    const all = document.querySelectorAll('video')
    expect(all.length).toBe(2)
    return all
  })
  // The layers render in a fixed order and A starts at the front, so the first
  // element is the one on screen and the second is the one being prepared.
  return { front: videos[0], back: videos[1] }
}

/**
 * Gives both layers a working playhead and a length.
 *
 * jsdom has no media pipeline: `currentTime` never moves and `duration` is NaN,
 * and the handover is driven entirely by those two — the replacement parks a
 * moment ahead of the playhead and the exchange happens when playback arrives.
 * Left as jsdom leaves them, the exchange can never be reached at all.
 */
function givePlayheads(front: HTMLVideoElement, back: HTMLVideoElement) {
  const clock = { front: 0, back: 0 }
  for (const [el, key] of [
    [front, 'front'],
    [back, 'back'],
  ] as const) {
    Object.defineProperty(el, 'currentTime', {
      configurable: true,
      get: () => clock[key],
      set: (v: number) => {
        clock[key] = v
      },
    })
    Object.defineProperty(el, 'duration', { configurable: true, get: () => 240 })
  }
  return clock
}

/** Folds the player into the corner by telling the observer the slot left view. */
async function minimise() {
  act(() => fireIntersection(screen.getByTestId('player-slot'), false))
  await settle()
}

describe('which layer decides whether it is playing', () => {
  it('ignores a play event from the layer being prepared', async () => {
    const { back } = await ready()
    expect(screen.getByLabelText('Play')).toBeInTheDocument()

    await act(async () => {
      fireEvent.play(back)
    })

    // Still Play: the back layer buffering ahead is not the viewer's playback.
    expect(screen.getByLabelText('Play')).toBeInTheDocument()
  })

  it('takes a play event from the layer on screen', async () => {
    const { front } = await ready()

    await act(async () => {
      fireEvent.play(front)
    })

    expect(screen.getByLabelText('Pause')).toBeInTheDocument()
  })

  it('takes a pause event from the layer on screen', async () => {
    const { front } = await ready()
    await act(async () => {
      fireEvent.play(front)
    })

    await act(async () => {
      fireEvent.pause(front)
    })

    expect(screen.getByLabelText('Play')).toBeInTheDocument()
  })

  it('ignores a pause event from the layer being prepared', async () => {
    const { front, back } = await ready()
    await act(async () => {
      fireEvent.play(front)
    })

    // This is what the handover does to the outgoing layer once it is no longer
    // the one on screen. It must not read as the viewer pausing.
    await act(async () => {
      fireEvent.pause(back)
    })

    expect(screen.getByLabelText('Pause')).toBeInTheDocument()
  })
})

describe('handing over to the downloaded file', () => {
  /**
   * The scenario the bug actually came from, driven end to end.
   *
   * The handover flips which layer is at the front and then calls play() on the
   * incoming one — all before React has re-rendered, so that element's props are
   * still the ones it had as the back layer. Binding the play handler to
   * render-time front-ness dropped that event, while the pause() on the outgoing
   * layer still landed, and the player was left reporting itself as stopped.
   *
   * Reproducing it needs the two element states jsdom will not supply on its
   * own: a playhead for the front layer, and a duration for the one being
   * prepared. Everything else is the real code path.
   */
  it('still reports playback after the local file takes over', async () => {
    const { front, back } = await ready()
    const clock = givePlayheads(front, back)

    await act(async () => {
      void front.play()
    })
    expect(screen.getByLabelText('Pause')).toBeInTheDocument()

    // The download finishes: the stream answer now offers a local file, and the
    // player prepares it on the back layer.
    localReady = true
    await waitFor(() => expect(back.getAttribute('src')).toBeTruthy(), { timeout: 8000 })

    // The replacement loads and parks itself a moment ahead of the playhead.
    await act(async () => {
      fireEvent.loadedMetadata(back)
    })
    await act(async () => {
      fireEvent.seeked(back)
    })

    // Playback reaches the mark the replacement is waiting on. This is the
    // trigger for the exchange, and without it the handover simply never runs —
    // which is how the first version of this test managed to pass against the
    // very bug it was written for.
    clock.front = clock.back
    await settle()

    // The exchange really happened: the prepared layer is the visible one now.
    expect(back.style.opacity).toBe('1')
    expect(front.style.opacity).toBe('0')

    // And the video is playing from the local file. Saying otherwise is the bug:
    // a play button over a running video, and controls pinned open behind it.
    expect(screen.getByLabelText('Pause')).toBeInTheDocument()
    // Generous, because the wait is real: the stream answer is re-polled every
    // five seconds while there is no local file, and that poll is what tells the
    // player the download has finished.
  }, 20000)
})

describe('controls offered by each shape', () => {
  beforeEach(() => {
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  })

  it('gives the full player everything', async () => {
    // The browser has to be able to do it before the button is drawn, so say so.
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    await ready()

    expect(screen.getByLabelText('Play')).toBeInTheDocument()
    expect(screen.getByLabelText('Seek')).toBeInTheDocument()
    expect(screen.getByLabelText('Volume')).toBeInTheDocument()
    expect(screen.getByLabelText('Subtitles')).toBeInTheDocument()
    expect(screen.getByLabelText('Full screen')).toBeInTheDocument()
  })

  it('gives the corner player play, volume and captions', async () => {
    await ready()
    await minimise()

    expect(screen.getByLabelText('Play')).toBeInTheDocument()
    expect(screen.getByLabelText('Volume')).toBeInTheDocument()
    expect(screen.getByLabelText('Subtitles')).toBeInTheDocument()
  })

  it('drops from the corner what will not fit or cannot mean anything there', async () => {
    await ready()
    await minimise()

    // No room, and full screen from a 400px window is a contradiction.
    expect(screen.queryByLabelText('Seek')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Full screen')).not.toBeInTheDocument()
  })

  it('still offers close and expand in the corner', async () => {
    await ready()
    await minimise()

    expect(screen.getByLabelText('Close player')).toBeInTheDocument()
    expect(screen.getAllByLabelText('Expand player').length).toBeGreaterThan(0)
  })

  it('keeps the controls out from under the click-to-expand surface', async () => {
    await ready()
    await minimise()

    // Weaker than it looks, and deliberately reported as such: whether one
    // element visually covers another is geometry, and jsdom has no layout —
    // getBoundingClientRect is all zeroes and elementFromPoint returns null.
    // This catches the controls being nested inside the expand button, which is
    // the version of the mistake a refactor is most likely to make. The overlap
    // itself is on the manual checklist, because it has to be.
    for (const label of ['Play', 'Volume', 'Subtitles']) {
      const control = screen.getByLabelText(label)
      for (const expand of screen.getAllByLabelText('Expand player')) {
        expect(expand.contains(control)).toBe(false)
      }
    }
  })
})

describe('telling the operating system what is playing', () => {
  it('publishes the video as media session metadata', async () => {
    const setActionHandler = vi.fn()
    vi.stubGlobal('MediaMetadata', class {
      constructor(public init: MediaMetadataInit) {}
    })
    Object.defineProperty(navigator, 'mediaSession', {
      configurable: true,
      value: { setActionHandler, metadata: null, playbackState: 'none' },
    })

    await ready()

    // This is what a phone's lock screen reads. Without it Android treats the
    // playback as untracked noise and is free to kill it on switching apps.
    const metadata = navigator.mediaSession.metadata as unknown as { init: MediaMetadataInit }
    expect(metadata.init.title).toBe('A video')
    expect(metadata.init.artist).toBe('A channel')

    const registered = setActionHandler.mock.calls.map(([action]) => action)
    expect(registered).toEqual(expect.arrayContaining(['play', 'pause']))
  })
})

describe('picture in picture', () => {
  it('is not drawn at all where the browser cannot do it', async () => {
    Object.defineProperty(document, 'pictureInPictureEnabled', {
      configurable: true,
      value: false,
    })

    await ready()

    // A button that cannot do anything is the one thing CLAUDE.md §5 forbids.
    expect(screen.queryByLabelText('Picture in picture')).not.toBeInTheDocument()
  })

  it('asks the visible layer for the floating window', async () => {
    Object.defineProperty(document, 'pictureInPictureEnabled', {
      configurable: true,
      value: true,
    })
    const request = vi.fn(async () => ({}) as PictureInPictureWindow)
    Object.defineProperty(HTMLVideoElement.prototype, 'requestPictureInPicture', {
      configurable: true,
      value: request,
    })

    const { front } = await ready()

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Picture in picture'))
    })

    expect(request).toHaveBeenCalled()
    expect(request.mock.instances[0]).toBe(front)
  })
})

describe('a finger, not a mouse', () => {
  const picture = () => document.querySelector('video')!
  const bar = () => document.querySelector('[data-player-controls]') as HTMLElement

  const visible = () => !bar().className.includes('opacity-0')

  it('does not hide the moment the finger lifts', async () => {
    // `pointerleave` fires when a touch ends, because the pointer has ceased to
    // exist rather than moved away. Acting on it took the controls away the
    // instant you stopped touching them — and, because the sequence is
    // pointerdown, pointerup, pointerleave, click, made the bar unclickable one
    // event before the click that was meant for it arrived.
    const { front } = await ready()
    await act(async () => {
      fireEvent.play(front)
    })

    await act(async () => {
      fireEvent.pointerDown(picture(), { pointerType: 'touch' })
    })
    expect(visible()).toBe(true)

    await act(async () => {
      fireEvent.pointerLeave(picture(), { pointerType: 'touch' })
    })
    expect(visible()).toBe(true)
  })

  it('still hides when a mouse leaves the picture', async () => {
    const { front } = await ready()
    await act(async () => {
      fireEvent.play(front)
    })

    await act(async () => {
      fireEvent.pointerDown(picture(), { pointerType: 'mouse' })
    })
    expect(visible()).toBe(true)

    await act(async () => {
      fireEvent.pointerLeave(picture(), { pointerType: 'mouse' })
    })
    expect(visible()).toBe(false)
  })

  it('a tap shows the controls instead of pausing', async () => {
    const { front } = await ready()
    await act(async () => {
      fireEvent.play(front)
    })
    // Hide them first, so the tap has something to do.
    await act(async () => {
      fireEvent.pointerLeave(picture(), { pointerType: 'mouse' })
    })
    expect(visible()).toBe(false)

    await act(async () => {
      fireEvent.pointerDown(picture(), { pointerType: 'touch' })
      fireEvent.click(picture())
    })

    expect(visible()).toBe(true)
    // Still playing: looking at the controls must not interrupt the video.
    expect(screen.getByLabelText('Pause')).toBeInTheDocument()
  })

  it('a second tap puts them away again', async () => {
    const { front } = await ready()
    await act(async () => {
      fireEvent.play(front)
    })
    // Start from hidden: the controls come up on load, so without this the
    // first tap would be putting them away and the test would be reading the
    // second tap as the first.
    await act(async () => {
      fireEvent.pointerLeave(picture(), { pointerType: 'mouse' })
    })

    await act(async () => {
      fireEvent.pointerDown(picture(), { pointerType: 'touch' })
      fireEvent.click(picture())
    })
    expect(visible()).toBe(true)

    await act(async () => {
      fireEvent.pointerDown(picture(), { pointerType: 'touch' })
      fireEvent.click(picture())
    })
    expect(visible()).toBe(false)
  })

  it('a mouse click still plays and pauses', async () => {
    const { front } = await ready()
    // Actually playing, not just an event saying so: `toggle` reads `paused`
    // from the element, and firing the event alone leaves it stopped.
    await act(async () => {
      void front.play()
    })
    expect(screen.getByLabelText('Pause')).toBeInTheDocument()

    await act(async () => {
      fireEvent.pointerDown(picture(), { pointerType: 'mouse' })
      fireEvent.click(picture())
    })

    expect(screen.getByLabelText('Play')).toBeInTheDocument()
  })
})

describe('a bar sized for a thumb', () => {
  /** Answers yes to `(pointer: coarse)` and no to everything else. */
  function pretendTouchDevice() {
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
  }

  afterEach(() => vi.unstubAllGlobals())

  it('drops the volume slider, which a phone has buttons for', async () => {
    pretendTouchDevice()
    await ready()

    expect(screen.queryByLabelText('Volume')).not.toBeInTheDocument()
  })

  it('keeps the volume slider where there is a mouse', async () => {
    await ready()

    expect(screen.getByLabelText('Volume')).toBeInTheDocument()
  })

  it('gathers the rest behind one settings button', async () => {
    pretendTouchDevice()
    await ready()

    // The switches that were loose on the bar are not on it any more.
    expect(screen.queryByLabelText('Autoplay')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Settings')).toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })
    // Autoplay only exists when there is a next video; narration is the row
    // this fixture can show.
    expect(screen.getByRole('switch', { name: 'Thuyết minh' })).toBeInTheDocument()
  })

  it('holds the controls open while the settings are open', async () => {
    // Otherwise the sheet takes itself away mid-decision: the bar hides on a
    // timer, and the sheet lives on the bar.
    pretendTouchDevice()
    const { front } = await ready()
    await act(async () => {
      fireEvent.play(front)
    })

    vi.useFakeTimers()
    try {
      const picture = document.querySelector('video')!
      act(() => {
        fireEvent.pointerDown(picture, { pointerType: 'touch' })
      })
      act(() => {
        fireEvent.click(screen.getByLabelText('Settings'))
      })

      // Well past the idle timeout, which on touch is five seconds.
      act(() => {
        vi.advanceTimersByTime(10_000)
      })

      const bar = document.querySelector('[data-player-controls]') as HTMLElement
      expect(bar.className).not.toContain('opacity-0')
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('the top bar', () => {
  it('offers no voice search, because there was never anything behind it', async () => {
    // It had no handler at all — a control that did nothing on either platform.
    await ready()
    expect(screen.queryByLabelText('Search by voice')).not.toBeInTheDocument()
  })
})

describe('room at the foot of the page', () => {
  const main = () => document.querySelector('main')!

  it('reserves none while the player is full size', async () => {
    await ready()
    expect(main().style.paddingBottom).toBe('')
  })

  it('reserves the miniplayer once it is in the corner', async () => {
    await ready()
    await minimise()

    // The last row of a grid is otherwise simply underneath the corner window,
    // and once the rubber band is gone there is no way to drag it into view.
    const reserved = Number.parseInt(main().style.paddingBottom, 10)
    expect(reserved).toBeGreaterThan(miniRectDesktop(window.innerWidth, window.innerHeight).height)
  })

  it('gives the room back when the player is closed', async () => {
    await ready()
    await minimise()
    expect(main().style.paddingBottom).not.toBe('')

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Close player'))
    })

    expect(main().style.paddingBottom).toBe('')
  })
})
