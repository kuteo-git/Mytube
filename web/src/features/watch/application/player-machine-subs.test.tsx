import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'

/**
 * Choosing the machine-translated Vietnamese track is itself a request to
 * translate.
 *
 * It was not treated as one. The pass ran only while read-aloud was on, and the
 * gateway attaches the track only once a file has been written — so the option
 * appeared after somebody had narrated the video, and could be filled by nothing
 * else. A viewer who simply wanted Vietnamese subtitles either found no option
 * at all, or found one that stayed almost empty: 68 translated batches in one
 * session left four lines on disk, because each short spell of read-aloud was
 * cancelled before it had covered much.
 */

const startTranslationPass = vi.fn()
const loadViSubtitles = vi.fn()

/** What the pass would report. Changed mid-test to see whether anyone is reading. */
let progress = {
  phase: 'idle' as string,
  done: 0,
  total: 0,
  etaSeconds: null as number | null,
  error: '',
  vttVersion: 0,
}

vi.mock('@/features/watch/application/narration', async () => {
  const actual = await vi.importActual<typeof import('./narration')>(
    '@/features/watch/application/narration',
  )
  return {
    ...actual,
    startTranslationPass: (videoId: string, from: number) =>
      startTranslationPass(videoId, from),
    loadViSubtitles: (url: string, lang: string) => loadViSubtitles(url, lang),
    narrationProgress: () => progress,
  }
})

/** Set per test: the tracks the video is published with. */
let subtitles: Array<{ language: string; label: string; url: string; generated: boolean }> = []

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

const baseVideo = {
  id: 'abc',
  title: 'An English video',
  channel,
  durationSeconds: 600,
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
  userState: {
    watchProgress: 0,
    watchPositionSeconds: 0,
    reaction: 'NONE' as const,
    inWatchLater: false,
  },
}

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getVideo: vi.fn(async () => ({ ...baseVideo, subtitles })),
    getVideoEnsuring: vi.fn(async () => ({ ...baseVideo, subtitles })),
    getStream: vi.fn(async () => ({
      local: { url: '/media/abc/1080p.mp4', height: 1080, name: 'local' },
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

const english = {
  language: 'en',
  label: 'English',
  url: '/media/abc/1080p.mp4.en.vtt',
  generated: false,
}
const humanVietnamese = {
  language: 'vi',
  label: 'Tiếng Việt',
  url: '/media/abc/1080p.mp4.vi.vtt',
  generated: false,
}

const settle = (ms = 20) => act(async () => void (await new Promise((r) => setTimeout(r, ms))))

beforeEach(() => {
  subtitles = [english]
  startTranslationPass.mockClear()
  loadViSubtitles.mockClear()
  progress = { phase: 'idle', done: 0, total: 0, etaSeconds: null, error: '', vttVersion: 0 }
  window.localStorage.clear()
})

async function openCaptionMenu() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/watch/abc']}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/watch/:videoId" element={<WatchPage />} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
  // The subtitle control lives behind the settings button here, as it does on a
  // touch bar: one panel rather than a row of separate menus.
  const settings = await waitFor(() => screen.getByLabelText('Settings'))
  await act(async () => {
    fireEvent.click(settings)
  })
  await settle()
}

describe('the machine-translated Vietnamese track', () => {
  it('is offered before any translation exists', async () => {
    await openCaptionMenu()

    // Nothing has been written yet — that is precisely when the option matters,
    // because choosing it is what writes it.
    expect(screen.getByText('VI (auto)')).toBeInTheDocument()
  })

  it('starts translating when it is chosen, with nothing else switched on', async () => {
    await openCaptionMenu()
    expect(startTranslationPass).not.toHaveBeenCalled()

    await act(async () => {
      fireEvent.click(screen.getByText('VI (auto)'))
    })
    await settle()

    // Read-aloud is off. Choosing the track is the whole request.
    await waitFor(() => expect(startTranslationPass).toHaveBeenCalledWith('abc', 0))
  })

  it('fetches the cues it is going to translate', async () => {
    await openCaptionMenu()

    await act(async () => {
      fireEvent.click(screen.getByText('VI (auto)'))
    })
    await settle()

    // The cues are what the pass translates, and they were fetched only when
    // read-aloud was on. Without them the pass started, found nothing to do and
    // reported "Not started" — indistinguishable from the track having done
    // nothing at all.
    await waitFor(() =>
      expect(loadViSubtitles).toHaveBeenCalledWith(english.url, 'en'),
    )
  })

  it('reports what the translator is doing, not "Not started" for ever', async () => {
    await openCaptionMenu()

    await act(async () => {
      fireEvent.click(screen.getByText('VI (auto)'))
    })
    // The pass gets under way.
    progress = { ...progress, phase: 'translating', done: 4, total: 900 }

    // Read only while narration was on, so without it the line kept the value
    // it was handed at mount — `idle`, which renders as "Not started". A
    // translation working away while the screen insists it has not begun is
    // worse than one that has not begun, because the next thing the viewer does
    // is press something else.
    await waitFor(
      () => expect(screen.queryByText('Not started')).not.toBeInTheDocument(),
      { timeout: 3000 },
    )
  })

  it('is not offered when a person has already written a Vietnamese track', async () => {
    subtitles = [english, humanVietnamese]
    await openCaptionMenu()

    // Translating over a human translation spends tokens to produce something
    // worse. The pass refuses this case too; the menu simply does not raise it.
    expect(screen.queryByText('VI (auto)')).not.toBeInTheDocument()
    expect(startTranslationPass).not.toHaveBeenCalled()
  })

  it('is not offered when there is no English to translate from', async () => {
    subtitles = [humanVietnamese]
    await openCaptionMenu()

    expect(screen.queryByText('VI (auto)')).not.toBeInTheDocument()
  })
})

describe('the Subtitles setting', () => {
  // A row reading "Off" with nothing to turn on is a control that cannot do
  // anything, which §5 does not allow. It was rendered unconditionally, so
  // every video with no captions carried one.
  it('is absent from a video with no subtitles', async () => {
    subtitles = []
    render(
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })}
      >
        <MemoryRouter initialEntries={['/watch/abc']}>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/watch/:videoId" element={<WatchPage />} />
            </Route>
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    )
    await waitFor(() => expect(document.querySelector('video')).not.toBeNull())
    await settle()

    // With no captions and one rendition there is nothing in the panel at all,
    // so the gear goes with it — but the assertion that matters is the row.
    const settings = screen.queryByLabelText('Settings')
    if (settings) {
      await act(async () => {
        fireEvent.click(settings)
      })
      await settle()
    }
    expect(screen.queryByText('Subtitles')).not.toBeInTheDocument()
  })

  it('is offered when the video carries a track', async () => {
    await openCaptionMenu()
    expect(screen.getByText('Subtitles')).toBeInTheDocument()
  })
})

describe('the speech status', () => {
  /**
   * Reading aloud builds an AudioContext, which jsdom does not have. Nothing
   * here listens to it; it only has to exist.
   */
  class FakeAudioContext {
    state = 'running'
    resume() {
      return Promise.resolve()
    }
    close() {
      return Promise.resolve()
    }
    suspend() {
      return Promise.resolve()
    }
  }

  beforeEach(() => {
    vi.stubGlobal('AudioContext', FakeAudioContext)
    // Read aloud, as though switched on in an earlier sitting.
    window.localStorage.setItem('yt-narration-speak-v1', '1')
  })

  // The reported fault. Synthesis progress was shown only where a translation
  // was also being made — so a video that came with Vietnamese of its own, the
  // one case where nothing has to be translated and the whole wait is
  // synthesis, reported nothing at all.
  it('is shown for a video whose Vietnamese was written by a person', async () => {
    subtitles = [humanVietnamese]
    await openCaptionMenu()

    expect(screen.getByText(/^Speech/)).toBeInTheDocument()
    // Nothing to translate, so that half says nothing.
    expect(screen.queryByText('Not started')).not.toBeInTheDocument()
  })

  it('still reports both halves when the Vietnamese has to be translated', async () => {
    subtitles = [english]
    await openCaptionMenu()

    expect(screen.getByText(/^Speech/)).toBeInTheDocument()
    expect(screen.getByText('Not started')).toBeInTheDocument()
  })
})
