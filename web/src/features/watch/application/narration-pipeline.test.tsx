import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'
import { narrationProgress } from '@/features/watch/application/narration'

const channel = {
  id: 'c1', name: 'A channel', handle: '@a', avatarPath: '', bannerPath: '',
  subscriberCount: 0, verified: false, subscribed: false,
}
const video = {
  id: 'abc', title: 'A video', channel, durationSeconds: 240, viewCount: 10,
  publishedAt: new Date().toISOString(), addedAt: new Date().toISOString(),
  thumbnailPath: '', description: '', hashtags: [], topics: [],
  mediaState: 'READY' as const, mediaPath: '', sizeBytes: 0, pinned: false,
  sourceUrl: '', likeCount: 0,
  subtitles: [{ language: 'en', label: 'English', url: '/media/abc/en.vtt', generated: false }],
  userState: { watchProgress: 0, watchPositionSeconds: 0, reaction: 'NONE' as const, inWatchLater: false },
}
const stream = {
  local: null, instant: { url: 'blob:instant', height: 360, name: 'instant' }, remux: null,
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

const seen: string[] = []

const VTT = `WEBVTT

00:00:01.000 --> 00:00:04.000
Hello there everyone.

00:00:05.000 --> 00:00:09.000
This is the second line of it.
`

beforeEach(() => {
  window.localStorage.clear()
  vi.stubGlobal('AudioContext', class {
    resume() { return Promise.resolve() }
    suspend() { return Promise.resolve() }
    close() { return Promise.resolve() }
  })
  seen.length = 0
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    seen.push(String(url))
    if (String(url).includes('/translate/config')) {
      return {
        ok: true,
        json: async () => ({ baseUrl: 'http://x', model: 'm1', hasKey: true, keyHint: '…1234' }),
      } as unknown as Response
    }
    if (String(url).endsWith('.vtt')) {
      return { ok: true, text: async () => VTT } as unknown as Response
    }
    if (String(url).includes('/translate/batch')) {
      const body = JSON.parse(String(init?.body ?? '{}'))
      return {
        ok: true,
        json: async () => ({
          translations: (body.cues ?? []).map((c: string) => 'VI:' + c),
        }),
      } as unknown as Response
    }
    return { ok: true, json: async () => ({ entries: {} }) } as unknown as Response
  }))
})

/**
 * The whole narration pipeline, wired the way the app wires it.
 *
 * Every part of this had unit tests and the chain between them still did not
 * run: the pass was cancelled by a layer swap, and when it did run and got
 * nothing back it reported itself as "not started". Both were invisible to
 * tests that only checked the pieces.
 */
describe('turning narration on', () => {
  it('loads cues, translates them, and writes all three artifacts', async () => {
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
    await waitFor(() => expect(document.querySelectorAll('video').length).toBe(2))

    await act(async () => { fireEvent.click(screen.getByLabelText('Settings')) })
    await act(async () => {
      fireEvent.click(screen.getByRole('switch', { name: 'Read aloud' }))
    })
    await act(async () => { await new Promise((r) => setTimeout(r, 50)) })

    for (let i = 0; i < 60 && narrationProgress().running; i++) {
      await act(async () => { await new Promise((r) => setTimeout(r, 20)) })
    }
    expect(narrationProgress()).toMatchObject({
      phase: 'done',
      done: 2,
      total: 2,
    })
    // The three artifacts that belong beside the video.
    expect(seen).toContain('/api/videos/abc/narration-cues')
    expect(seen).toContain('/api/videos/abc/narration-cache')
    expect(seen).toContain('/api/videos/abc/narration-vtt')
  })
})
