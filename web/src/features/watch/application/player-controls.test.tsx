import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { HomePage } from '@/pages/HomePage'
import { WatchPage } from '@/pages/WatchPage'
import { miniRectDesktop } from '@/features/watch/application/player-geometry'
import { fireIntersection } from '@/test/setup'
import {
  forgetLastWatched,
  readLastWatched,
  rememberLastWatched,
} from '@/features/watch/application/last-watched'

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

/** Chips the home page offers. Empty except where a test needs one to click. */
let topicList: Array<{ name: string; videoCount: number }> = []

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
    listUpNext: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    listPopular: vi.fn(async () => []),
    listComments: vi.fn(async () => ({ comments: [], nextPageToken: '' })),
    listTopics: vi.fn(async () => topicList),
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

const settle = (ms = 20) => act(async () => void (await new Promise((r) => setTimeout(r, ms))))

beforeEach(() => {
  localReady = false
  // Narration preferences persist. A test that switches narration on would
  // otherwise leave it on for every test after it in this file, and those
  // render a player that starts an audio scheduler jsdom has no clock for.
  window.localStorage.removeItem('yt-narration-speak-v1')
  window.localStorage.removeItem('yt-narration-auto-translate-v1')
  window.localStorage.removeItem('yt-narration-output-v1')
  window.localStorage.removeItem('yt-narration-engine-v1')
  window.localStorage.removeItem('yt-narration-on')
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
    expect(screen.getByLabelText('Full screen')).toBeInTheDocument()
    // Captions are behind the gear now, beside the narration settings, rather
    // than a button of their own a few pixels away from them.
    expect(screen.queryByLabelText('Subtitles')).not.toBeInTheDocument()
    expect(screen.getByLabelText('Settings')).toBeInTheDocument()
    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })
    expect(screen.getByRole('radio', { name: 'EN' })).toBeInTheDocument()
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

    // The button appears once an effect has asked the element whether it can
    // float — the prototype only says the browser knows the idea. So it is
    // waited for rather than assumed, which is what made this flaky.
    const button = await screen.findByLabelText('Picture in picture')
    await act(async () => {
      fireEvent.click(button)
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
    // this fixture can show. Reading aloud is a switch again — what is written
    // on the picture is the segmented control beside it.
    expect(
      screen.getByRole('switch', { name: 'Vietnamese narration' }),
    ).toBeInTheDocument()
  })

  it('puts captions behind the gear too', async () => {
    pretendTouchDevice()
    await ready()

    expect(screen.queryByLabelText('Subtitles')).not.toBeInTheDocument()

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })
    // Captions are a segmented control too, restricted to the two languages
    // this library narrates between.
    const group = screen.getByRole('radiogroup', { name: 'Subtitles' })
    expect(within(group).getByRole('radio', { name: 'EN' })).toBeInTheDocument()
    expect(within(group).getByRole('radio', { name: 'Off' })).toBeInTheDocument()
  })

  it('offers the narration settings to a mouse as well', async () => {
    // These first went only into the touch branch of the gear's extras, which
    // renders nothing when there is a mouse — so with a mouse the engine choice
    // and the subtitle-only mode could not be reached at all, while every test
    // still passed. The headphone button on the bar toggles narration but
    // cannot say which engine translates it.
    await ready()

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })
    // One subtitle control and one switch, not two overlapping groups.
    expect(
      screen.getByRole('radiogroup', { name: 'Subtitles' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('switch', { name: 'Vietnamese narration' }),
    ).toBeInTheDocument()
  })

  it('reports what the translator is doing once narration is on', async () => {
    // The engine choice is gone — there is one translator now. What the panel
    // still owes the viewer is whether it is working, since the pass runs for
    // minutes in silence and "still going" and "broken" look identical.
    vi.stubGlobal(
      'AudioContext',
      class {
        resume() {
          return Promise.resolve()
        }
        suspend() {
          return Promise.resolve()
        }
        close() {
          return Promise.resolve()
        }
      },
    )
    await ready()

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })
    expect(screen.queryByText(/Translating|Loading subtitles|Not started/)).toBeNull()

    await act(async () => {
      fireEvent.click(screen.getByRole('switch', { name: 'Vietnamese narration' }))
    })
    expect(
      screen.getByText(/Translating|Loading subtitles|Not started|Translation failed/),
    ).toBeInTheDocument()
  })

  it('puts the settings groups in a fixed order', async () => {
    // Resolution, subtitles, read aloud, autoplay, then translation behind a
    // rule. Translation is last because it is the only one describing work
    // being done rather than a preference.
    //
    // No sound settings among them. The equaliser and the room have their own
    // button beside the gear: this menu is what belongs to the video, and those
    // belong to the speakers the viewer is sitting in front of.
    await ready()
    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })

    const groups = screen
      .getAllByRole('radiogroup')
      .map((g) => g.getAttribute('aria-label'))
    expect(groups).toEqual(['Resolution', 'Subtitles'])

    const switches = screen.getAllByRole('switch').map((b) => b.textContent)
    expect(switches[0]).toContain('Vietnamese narration')
    // And no translation switch at the end of them. Translation is asked for by
    // choosing the track or by asking for the narration; a third control only
    // qualified those two, and being on by default it read as broken whichever
    // way it was pressed.
    expect(switches.some((s) => s?.includes('Auto translate'))).toBe(false)
  })

  it('shows the translation progress only while a translation is wanted', async () => {
    // The line reports work, so it belongs on screen exactly while there is
    // work: nothing has been asked for until the narration is.
    vi.stubGlobal(
      'AudioContext',
      class {
        resume() {
          return Promise.resolve()
        }
        suspend() {
          return Promise.resolve()
        }
        close() {
          return Promise.resolve()
        }
      },
    )
    await ready()
    await act(async () => {
      fireEvent.click(screen.getByLabelText('Settings'))
    })
    expect(
      screen.queryByText(/Translating|Loading subtitles|Not started/),
    ).toBeNull()

    await act(async () => {
      fireEvent.click(screen.getByRole('switch', { name: 'Vietnamese narration' }))
    })
    expect(
      screen.getByText(/Translating|Loading subtitles|Not started/),
    ).toBeInTheDocument()

    // And it goes again with it, rather than reporting on a pass that has been
    // cancelled.
    await act(async () => {
      fireEvent.click(screen.getByRole('switch', { name: 'Vietnamese narration' }))
    })
    expect(
      screen.queryByText(/Translating|Loading subtitles|Not started/),
    ).toBeNull()
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

describe('closing the corner player from the watch page', () => {
  it('reaches the close button rather than the surface behind it', async () => {
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    const { front } = await ready()
    await act(async () => {
      void front.play()
    })
    await minimise()

    // Wake the chrome, or the button is deliberately unclickable.
    await act(async () => {
      fireEvent.pointerMove(document.querySelector('video')!, { pointerType: 'mouse' })
    })

    const close = screen.getByLabelText('Close player')
    // The click-to-expand surface covers the picture. If the close button were
    // inside it, or under it, this press would expand instead of closing.
    for (const expand of screen.getAllByLabelText('Expand player')) {
      expect(expand.contains(close)).toBe(false)
    }

    await act(async () => {
      fireEvent.click(close)
    })

    // Closing on the watch page returns the player to its slot and pauses it,
    // rather than destroying it — the video still has a home on that page.
    expect(document.querySelector('video')).toBe(front)
    expect(screen.getByTestId('player-host').style.position).toBe('absolute')
  })
})

describe('the subtitle preference and the two layers', () => {
  /**
   * jsdom builds no TextTrack objects for `<track>` children, so the modes the
   * effect writes have nowhere to land. They are given somewhere: what is being
   * checked is which elements get written to, not what a browser does with it.
   */
  function trackable(video: HTMLVideoElement, languages: string[]) {
    const tracks = languages.map((language) => ({ language, mode: 'showing', cues: null }))
    Object.defineProperty(video, 'textTracks', {
      configurable: true,
      value: Object.assign(tracks, {
        length: tracks.length,
        addEventListener() {},
        removeEventListener() {},
      }),
    })
    return tracks
  }

  it('reaches the layer that is not on screen', async () => {
    // The reported fault. Applying it to the front element alone left the other
    // holding whatever it was last told — so turning subtitles off while B was
    // in front left A still `showing`, and the next tier climb brought A back
    // with the subtitles switched off and visible. Only a video still
    // downloading swaps layers, which is why it looked like a fault of new
    // videos.
    await ready()
    const [a, b] = Array.from(document.querySelectorAll('video'))
    const back = trackable(b, ['en'])
    trackable(a, ['en'])

    await act(async () => {
      await new Promise((r) => setTimeout(r, 60))
    })

    // Captions default to off, and both layers have to say so.
    expect(back[0].mode).toBe('disabled')
  })

  /**
   * As `trackable`, but the list can be listened to and fired at — which is
   * what a browser does when it selects a track by itself.
   */
  function liveTrackable(video: HTMLVideoElement, languages: string[]) {
    const listeners: Record<string, Array<() => void>> = {}
    const tracks = languages.map((language) => ({ language, mode: 'disabled', cues: null }))
    Object.defineProperty(video, 'textTracks', {
      configurable: true,
      value: Object.assign(tracks, {
        length: tracks.length,
        addEventListener(type: string, fn: () => void) {
          ;(listeners[type] ??= []).push(fn)
        },
        removeEventListener(type: string, fn: () => void) {
          listeners[type] = (listeners[type] ?? []).filter((f) => f !== fn)
        },
      }),
    })
    return {
      tracks,
      fire: (type: string) => {
        for (const fn of listeners[type] ?? []) fn()
      },
    }
  }

  it('puts back a track the browser switched on by itself', async () => {
    // The reported fault: open a new video, wait for the file to land, and
    // English subtitles appear while the setting still reads Off. Nothing in
    // this app asks for that — a browser may select a track on its own when one
    // is attached, which for a video being downloaded is long after the effect
    // that applies the preference last ran. So the preference is enforced, not
    // applied once.
    await ready()
    const [a, b] = Array.from(document.querySelectorAll('video'))
    liveTrackable(b, ['en'])
    const front = liveTrackable(a, ['en'])

    await act(async () => {
      await new Promise((r) => setTimeout(r, 60))
    })

    await act(async () => {
      front.tracks[0].mode = 'showing'
      front.fire('change')
    })

    expect(front.tracks[0].mode).toBe('disabled')
  })
})

describe('coming back from Apple’s full-screen player', () => {
  const enterFullscreen = (video: HTMLVideoElement) =>
    act(() => {
      video.dispatchEvent(new Event('webkitbeginfullscreen'))
    })

  const leaveFullscreen = (video: HTMLVideoElement) =>
    act(() => {
      video.dispatchEvent(new Event('webkitendfullscreen'))
    })

  it('is still playing when the system stops it before announcing the exit', async () => {
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    const { front } = await ready()
    await act(async () => {
      void front.play()
    })

    await enterFullscreen(front)
    await act(async () => {
      front.pause()
    })
    await leaveFullscreen(front)

    expect(screen.getByLabelText('Pause')).toBeInTheDocument()
  })

  it('is still playing when the system stops it after announcing the exit', async () => {
    // The ordering the first attempt missed: at the moment the exit is
    // announced the video has not stopped yet, so looking at it then finds
    // nothing wrong and the pause lands unopposed a moment later.
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    const { front } = await ready()
    await act(async () => {
      void front.play()
    })

    await enterFullscreen(front)
    await leaveFullscreen(front)
    await act(async () => {
      front.pause()
    })

    expect(screen.getByLabelText('Pause')).toBeInTheDocument()
  })

  it('remembers across a source change made while full screen', async () => {
    // The download finishing mid-viewing re-runs the effect, and the memory of
    // "it was playing" cannot be the one that goes with it.
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    const { front, back } = await ready()
    await act(async () => {
      void front.play()
    })

    await enterFullscreen(front)
    localReady = true
    await waitFor(() => expect(back.getAttribute('src')).toBeTruthy(), { timeout: 8000 })
    await leaveFullscreen(front)
    await act(async () => {
      front.pause()
    })

    expect(screen.getByLabelText('Pause')).toBeInTheDocument()
  }, 20000)

  it('keeps playing when it was started inside the system player', async () => {
    // The reported fault. The first version decided on the way *in*: enlarging
    // a stopped video armed nothing, so pressing play inside the system player
    // and swiping back out returned a still frame — the decision had been taken
    // before any of it happened. What the viewer leaves it doing is what it
    // should go on doing.
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    const { front } = await ready()

    await enterFullscreen(front)
    await act(async () => {
      void front.play()
    })
    await leaveFullscreen(front)
    await act(async () => {
      front.pause()
    })

    expect(screen.getByLabelText('Pause')).toBeInTheDocument()
  })

  it('stays stopped when the viewer stopped it inside the system player', async () => {
    // The other half of the same rule, and what stops "keep playing" from
    // becoming "always play". Told apart from the system letting go by when it
    // happened: the system stops the video in the same breath as the exit, a
    // viewer some moments earlier.
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    const { front } = await ready()
    await act(async () => {
      void front.play()
    })

    await enterFullscreen(front)
    await act(async () => {
      front.pause()
    })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 200))
    })
    await leaveFullscreen(front)

    expect(screen.getByLabelText('Play')).toBeInTheDocument()
  })

  it('honours a second pause right after the exit', async () => {
    // Exactly one pause is swallowed. The system lets go once; a second this
    // close behind is somebody who really did press stop, and swallowing that
    // would make the button look broken.
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    const { front } = await ready()
    await act(async () => {
      void front.play()
    })

    await enterFullscreen(front)
    await leaveFullscreen(front)
    await act(async () => {
      front.pause()
    })
    await act(async () => {
      front.pause()
    })

    expect(screen.getByLabelText('Play')).toBeInTheDocument()
  })

  it('honours a pause the viewer asked for after coming back', async () => {
    // What tells the system's stop from the viewer's, and the reason it is not
    // a stopwatch. Measured on a real iPhone the system's stop landed at 289ms,
    // 291ms and 299ms across three runs — a 250ms window missed all of them,
    // and a window wide enough to catch them would start swallowing pauses the
    // viewer meant. Nobody touching anything is the honest question.
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    const { front } = await ready()
    await act(async () => {
      void front.play()
    })

    await enterFullscreen(front)
    await leaveFullscreen(front)
    // A hand arrives, and what follows is a decision rather than the system.
    await act(async () => {
      document.dispatchEvent(new Event('pointerdown', { bubbles: true }))
      front.pause()
    })

    expect(screen.getByLabelText('Play')).toBeInTheDocument()
  })

  it('leaves it stopped if it was stopped when it went in', async () => {
    Object.defineProperty(document, 'fullscreenEnabled', { configurable: true, value: true })
    const { front } = await ready()

    await enterFullscreen(front)
    await leaveFullscreen(front)

    expect(screen.getByLabelText('Play')).toBeInTheDocument()
  })
})

describe('when the browser refuses to start it', () => {
  it('leaves the first frame still rather than playing without sound', async () => {
    // CLAUDE.md §8b: a video that is plainly not running is easier to
    // understand than one that appears to be running with nothing to hear. The
    // code had drifted into muting and carrying on, which on iPhone is not an
    // edge case — Safari refuses audible autoplay almost always, so silent was
    // simply what playback sounded like.
    const reject = vi
      .spyOn(HTMLMediaElement.prototype, 'play')
      .mockRejectedValue(new Error('NotAllowedError'))

    const { front } = await ready()
    await act(async () => {
      fireEvent.loadedMetadata(front)
    })

    expect(front.muted).toBe(false)
    expect(screen.getByLabelText('Play')).toBeInTheDocument()

    reject.mockRestore()
  })
})

describe('picking up where the tab left off', () => {
  function renderHome() {
    // A real cache lifetime, unlike the rest of this file. It matters here: with
    // gcTime 0 the video is thrown away the moment nothing is observing it, so
    // a second offer would have to refetch and would not arrive in time to be
    // seen. In production the data is still there and the second offer is
    // instant — which is the behaviour being guarded against.
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={['/']}>
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

  afterEach(() => forgetLastWatched())

  it('offers the video back in the corner, stopped', async () => {
    rememberLastWatched('abc', 42, 240)
    const play = vi.spyOn(HTMLMediaElement.prototype, 'play')

    renderHome()
    const element = await waitFor(() => {
      const el = document.querySelector('video')
      expect(el).not.toBeNull()
      return el!
    })

    // In the corner, because this is not the watch page.
    expect(screen.getByTestId('player-host').style.position).toBe('fixed')

    await act(async () => {
      fireEvent.loadedMetadata(element)
    })

    // And waiting to be accepted. Sound arriving unbidden from the corner of a
    // page somebody has only just opened is startling.
    expect(play).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Play')).toBeInTheDocument()

    play.mockRestore()
  })

  it('offers nothing when nothing was left unfinished', async () => {
    renderHome()
    await settle()
    expect(document.querySelector('video')).toBeNull()
  })

  it('can be refused, and stays refused', async () => {
    // The offer used to put itself straight back. Closing clears the player's
    // state, which is precisely the condition the resume waits for, and the
    // entry it reads is a snapshot taken at mount — so clearing storage did not
    // stop it either. The close button worked and was undone in the same tick.
    rememberLastWatched('abc', 42, 240)
    renderHome()
    await waitFor(() => expect(document.querySelector('video')).not.toBeNull())

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Close player'))
    })
    // Long enough for the offer to be made a second time if it were going to
    // be: clearing the player's state is the very condition it waits for.
    await settle(300)

    expect(document.querySelector('video')).toBeNull()
  })

  it('leaves room for the corner window before it arrives', async () => {
    // Otherwise the page is drawn, and then three hundred pixels of layout move
    // underneath the viewer as the player turns up.
    rememberLastWatched('abc', 42, 240)
    renderHome()

    const main = document.querySelector('main')!
    expect(main.style.paddingBottom).not.toBe('')

    await waitFor(() => expect(document.querySelector('video')).not.toBeNull())
    // And the room does not change when it actually arrives.
    expect(main.style.paddingBottom).not.toBe('')
  })

  it('stops offering once the viewer closes it', async () => {
    rememberLastWatched('abc', 42, 240)
    renderHome()
    await waitFor(() => expect(document.querySelector('video')).not.toBeNull())

    await act(async () => {
      fireEvent.click(screen.getByLabelText('Close player'))
    })

    // Closing it is the answer to the offer, and it must not be made again.
    expect(readLastWatched()).toBeNull()
  })
})

describe('changing topic on the home page', () => {
  async function renderChips() {
    topicList = [{ name: 'Music', videoCount: 3 }]
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter initialEntries={['/']}>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/" element={<HomePage />} />
            </Route>
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    await settle()
  }

  afterEach(() => {
    topicList = []
  })

  it('starts the new grid at its own beginning', async () => {
    // Rewritten: this used to render a topic route and assert that mounting
    // scrolled to the top, which is not what its name claims and is not what
    // the feature is for. A chip is local state rather than navigation, so it
    // has to be pressed for real.
    await renderChips()
    // <main> is what scrolls now, not the window, and jsdom implements neither.
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo

    await act(async () => {
      fireEvent.click(screen.getByRole('tab', { name: 'Music' }))
    })

    // Keeping the position across the change left the viewer partway down a
    // list they had not seen the top of, and often past the end of it.
    expect(scrollTo).toHaveBeenCalledWith({ top: 0 })
  })

  it('does not reset the scroll merely by being opened', async () => {
    // The bug this pair exists to separate. Mounting is what happens when you
    // come back to Home from another tab, and useScrollRestoration has just put
    // the viewer back where they were — so a scroll-to-top here undoes it a
    // frame later, and the scroll memory looks broken on the page people scroll
    // most.
    const scrollTo = vi.fn()
    Element.prototype.scrollTo = scrollTo
    await renderChips()

    expect(scrollTo).not.toHaveBeenCalledWith({ top: 0 })
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
