import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'
import { isAttached } from '@/features/watch/application/audio-graph'

/**
 * That the player's video actually reaches the audio graph.
 *
 * Written after a bug with no symptom worth the name. The two `<video>` layers
 * live behind `playable`, so they are not in the tree on the first render — and
 * they were attached by an effect with an empty dependency list, which therefore
 * ran once against two nulls and never again. Everything else worked: the
 * context was built, the filters were created, every slider wrote its value into
 * a `BiquadFilterNode`. No signal passed through any of them, so the equaliser
 * moved nothing, and an unattached element plays perfectly well by itself — the
 * whole failure was "the sound does not change", with nothing in the console and
 * nothing in the tests.
 *
 * The routing is now done by the elements' own ref callbacks, which is what this
 * holds in place.
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
  },
}))

/** Enough Web Audio for the graph to build; see audio-graph.test.ts for the rest. */
class FakeParam {
  value = 0
  setTargetAtTime(v: number) {
    this.value = v
  }
  setValueAtTime(v: number) {
    this.value = v
  }
}
class FakeNode {
  connect(node: unknown) {
    return node
  }
  disconnect() {}
}
class FakeGain extends FakeNode {
  gain = new FakeParam()
}
class FakeFilter extends FakeNode {
  type = ''
  frequency = new FakeParam()
  Q = new FakeParam()
  gain = new FakeParam()
}
class FakeContext {
  state: AudioContextState = 'running'
  currentTime = 0
  destination = new FakeNode()
  resume = vi.fn(async () => {})
  addEventListener = vi.fn()
  private sources = new WeakSet<object>()
  createGain() {
    return new FakeGain()
  }
  createBiquadFilter() {
    return new FakeFilter()
  }
  createMediaElementSource(el: object) {
    if (this.sources.has(el)) throw new Error('already connected')
    this.sources.add(el)
    return new FakeNode()
  }
}

beforeEach(() => {
  window.localStorage.clear()
  vi.stubGlobal('AudioContext', FakeContext)
})

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

describe('the player and the audio graph', () => {
  it('routes both layers once they exist, not once at mount', async () => {
    renderWatch()
    const videos = await waitFor(() => {
      const all = document.querySelectorAll('video')
      expect(all.length).toBe(2)
      return all
    })
    await act(async () => void (await new Promise((r) => setTimeout(r, 20))))

    // The layers appear only after a stream resolves. Both of them, because the
    // hidden one comes to the front on every handover and would otherwise arrive
    // outside the graph — which is silence, not a missing equaliser.
    expect(isAttached(videos[0] as HTMLVideoElement)).toBe(true)
    expect(isAttached(videos[1] as HTMLVideoElement)).toBe(true)
  })
})
