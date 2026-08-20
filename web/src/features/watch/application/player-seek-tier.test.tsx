import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'
import { WatchPage } from '@/pages/WatchPage'

/**
 * How a seek is served while the video is still being muxed live.
 *
 * The muxed stream carries no index, so it cannot be moved within: a seek means
 * opening a new one at the mark. That is expensive, and — as this file's
 * subject — it was also broken. A replacement prepared for a seek is not an
 * upgrade, so the handover cannot wait for the playhead to reach it and waited
 * for the stream to buffer instead; on a stream that never satisfied that test
 * the wait had no end, and a pinned 1080p had no timeout either. What the
 * viewer saw was "Seeking…" and then their video still sitting where it was.
 *
 * The odd part of the report was the clue: seeking in the low rendition and
 * *then* switching to 1080p worked perfectly. Two ways of asking for the same
 * picture, one working and one not. So a seek now takes the road that works —
 * down to the low rendition, across to the mark, and back up.
 */

const REMUX_URL = '/api/videos/abc/remux'
const INSTANT_URL = 'blob:instant'

/** Set per test: whether YouTube publishes a progressive rendition to detour through. */
let hasInstant = true
/** Where the server says a mux opened at `t` will really begin. */
let keyframeFor: (at: number) => number = (at) => at - 2.028

const getRemuxStart = vi.fn(async (_id: string, at: number) => keyframeFor(at))

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
  title: 'A long video',
  channel,
  durationSeconds: 2400,
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
    // No local file: this is the state the bug lives in, a long video still
    // being fetched while it is being watched.
    getStream: vi.fn(async () => ({
      local: null,
      instant: hasInstant ? { url: INSTANT_URL, height: 360, name: 'instant' } : null,
      remux: { url: REMUX_URL, height: 1080, name: 'remux' },
    })),
    getRemuxStart: (id: string, at: number) => getRemuxStart(id, at),
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

const settle = (ms = 20) => act(async () => void (await new Promise((r) => setTimeout(r, ms))))

beforeEach(() => {
  hasInstant = true
  keyframeFor = (at) => at - 2.028
  getRemuxStart.mockClear()
  window.localStorage.clear()
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

/**
 * jsdom has no media pipeline: currentTime never moves and duration is NaN, and
 * both drive the handover. Left alone, no exchange can ever happen.
 */
function givePlayheads(elements: NodeListOf<HTMLVideoElement>) {
  const clock = new WeakMap<HTMLVideoElement, number>()
  for (const el of elements) {
    clock.set(el, 0)
    // How much of this element has arrived. Enough by default; a test that
    // cares sets it to nothing.
    Object.defineProperty(el, 'buffered', {
      configurable: true,
      writable: true,
      value: { length: 1, start: () => 0, end: () => 1e6 },
    })
    Object.defineProperty(el, 'currentTime', {
      configurable: true,
      get: () => clock.get(el) ?? 0,
      set: (v: number) => clock.set(el, v),
    })
    Object.defineProperty(el, 'duration', { configurable: true, get: () => 2400 })
    // jsdom never actually plays, so `paused` would stay true and the handover
    // would take its "nothing to wait for" branch in every test.
    Object.defineProperty(el, 'paused', { configurable: true, writable: true, value: false })
    // The seek handover commits once the element has data where it sits.
    Object.defineProperty(el, 'readyState', { configurable: true, get: () => 2 })
  }
}

async function mounted() {
  renderWatch()
  const videos = await waitFor(() => {
    const all = document.querySelectorAll('video')
    expect(all.length).toBe(2)
    return all as NodeListOf<HTMLVideoElement>
  })
  givePlayheads(videos)
  await settle()
  return videos
}

/**
 * The element the viewer is looking at. The two layers are permanently mounted
 * and exchanged by opacity, so which is which is a matter of style rather than
 * of position in the tree.
 */
function visible(videos: NodeListOf<HTMLVideoElement>) {
  return Array.from(videos).find((v) => v.getAttribute('aria-hidden') !== 'true') ?? videos[0]
}

function hidden(videos: NodeListOf<HTMLVideoElement>) {
  return Array.from(videos).find((v) => v.getAttribute('aria-hidden') === 'true') ?? videos[1]
}

/** Completes whatever handover is in flight by telling the hidden layer it loaded. */
async function loadHiddenLayer(videos: NodeListOf<HTMLVideoElement>) {
  await act(async () => {
    fireEvent.loadedMetadata(hidden(videos))
  })
  await settle()
}

/** Climbs from the opening low rendition to the muxed stream, as auto does. */
async function climbToRemux(videos: NodeListOf<HTMLVideoElement>) {
  await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
  // The handover waits for the playhead to reach the mark the replacement is
  // parked on, and jsdom's clock never moves on its own. Just past it, not far
  // past it: the muxed stream cannot be seeked, so a viewer more than a second
  // beyond its mark is no longer somewhere it can be handed over at.
  visible(videos).currentTime = 18.5
  await loadHiddenLayer(videos)
  await waitFor(() => expect(visible(videos).src).toContain('/remux'))
}

async function seekTo(seconds: number) {
  const bar = screen.getByLabelText('Seek')
  await act(async () => {
    fireEvent.change(bar, { target: { value: String(seconds) } })
    fireEvent.pointerUp(bar, { target: { value: String(seconds) } })
  })
  await settle()
}

describe('seeking while the video is muxed live', () => {
  it('goes through the low rendition rather than reopening the mux', async () => {
    const videos = await mounted()
    await climbToRemux(videos)
    getRemuxStart.mockClear()

    await seekTo(600)

    // The replacement being prepared is the progressive rendition, which seeks
    // natively — not another mux, which is what used to hang.
    expect(hidden(videos).src).toContain(INSTANT_URL)
    expect(hidden(videos).src).not.toContain('/remux')
    expect(getRemuxStart).not.toHaveBeenCalled()

    await loadHiddenLayer(videos)

    // And the viewer is at the mark, in the low rendition, playing.
    const front = visible(videos)
    expect(front.src).toContain(INSTANT_URL)
    expect(front.currentTime).toBe(600)
  })

  it('climbs back to full resolution from the new position, on a short lead', async () => {
    const videos = await mounted()
    await climbToRemux(videos)
    getRemuxStart.mockClear()

    await seekTo(600)
    await loadHiddenLayer(videos)
    await settle()

    // Five seconds ahead, not twenty. Twenty is the lead for the first climb of
    // a video whose network behaviour has not been seen yet; by now the stream
    // has been opened once and the cost is known, and a viewer who moves the
    // bar should not owe a third of a minute of 360p for it.
    await waitFor(() => expect(getRemuxStart).toHaveBeenCalledWith('abc', 605))
    await waitFor(() => expect(hidden(videos).src).toContain('t=605.000'))
  })

  it('tells the mux where to seek the audio, or the sound runs ahead', async () => {
    const videos = await mounted()
    await climbToRemux(videos)

    await seekTo(600)
    await loadHiddenLayer(videos)

    // An input seek lands on the nearest keyframe at or before the mark, and the
    // two inputs do not land in the same place — measured at 2.008s of sound
    // ahead of picture. The audio has to be sent to the video's keyframe, so the
    // player passes it on rather than letting the server guess twice.
    await waitFor(() => expect(hidden(videos).src).toContain(`audioAt=${(605 - 2.028).toFixed(3)}`))
  })

  it('keeps the detour when 1080p is pinned', async () => {
    window.localStorage.setItem('quality', 'high')
    const videos = await mounted()
    // Pinned high opens straight on the mux; there is nothing to climb.
    await waitFor(() => expect(visible(videos).src).toContain('/remux'))

    await seekTo(600)

    // Pinning is an instruction about which picture to end on, not a demand to
    // be left waiting on the one road that could not deliver it.
    expect(hidden(videos).src).toContain(INSTANT_URL)

    await loadHiddenLayer(videos)
    expect(visible(videos).currentTime).toBe(600)

    // And it goes back up, because that is what pinning asked for.
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
  })

  it('stays on the low rendition when 360p is pinned', async () => {
    window.localStorage.setItem('quality', 'low')
    const videos = await mounted()
    await settle(50)

    // Nothing climbs: a pinned choice is an instruction, and the mux is never
    // opened at all.
    expect(hidden(videos).src).not.toContain('/remux')
    expect(getRemuxStart).not.toHaveBeenCalled()
  })

  it('reopens the mux directly when there is no low rendition to detour through', async () => {
    hasInstant = false
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain('/remux'))

    await seekTo(600)

    // The only road left. It is the fallback rather than the rule because it is
    // the slow one, not because it is wrong.
    expect(hidden(videos).src).toContain('t=600.000')
    await loadHiddenLayer(videos)
    expect(visible(videos).src).toContain('t=600.000')
  })

  it('lands at the keyframe rather than seeking the last two seconds of an unindexed stream', async () => {
    hasInstant = false
    const videos = await mounted()
    await waitFor(() => expect(visible(videos).src).toContain('/remux'))

    await seekTo(600)
    await loadHiddenLayer(videos)

    // The stream really begins at 597.972 — ffmpeg cannot cut between keyframes
    // without re-encoding, which this design will not do.
    //
    // This used to close that gap by moving the element the remaining 2.028s,
    // on the reasoning that a seek *within what has already arrived* is one
    // even an unindexed stream allows. That reasoning is what CLAUDE.md §4 was
    // later written against, and the measurement it records is this exact
    // arithmetic: the browser failed on the audio packet at `0.766259` — which
    // is 19.70 minus 18.936, a mark minus its keyframe — and reported
    // `PIPELINE_ERROR_DECODE: Failed to send audio packet`, on four videos at
    // four different marks. The number in the error was the number the player
    // had just written. Buffered is not the same as seekable.
    //
    // The guard was added to `catchUpToViewer` at the time and not here, and
    // the comment left behind claimed this case was safe. It is the same
    // computation on the same kind of stream.
    //
    // So the viewer lands two seconds early. That is the cost §4 already chose
    // — "costing a mux rather than the sound" — and it is not a lie: the
    // position is read from the stream's own origin, so the bar says 597.972
    // and means it.
    await waitFor(() => expect(visible(videos).currentTime).toBe(0))
  })

  it('reads the position from one source, not from two added together', async () => {
    const videos = await mounted()
    await climbToRemux(videos)

    await seekTo(600)
    await loadHiddenLayer(videos)

    // 10:00, not 20:00. The clock is `offset + currentTime`, so a handover that
    // applies a claim belonging to a different source counts the position
    // twice: observed on a real seek to 2059.5s, which then reported 4130s —
    // the low rendition's playhead plus the muxed stream's keyframe. The
    // element's own src is what decides which claim may be applied.
    await waitFor(() => expect(screen.getByText(/^10:00 \//)).toBeInTheDocument())

    // And it must still know it is on the low rendition, or nothing will ever
    // climb again: the tier it believes it is on would be the tier it wants.
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
  })

  it('gives up a late climb rather than seeking a stream that has no index', async () => {
    const videos = await mounted()

    // Preparation ran late and the playhead is well past the mark. Winding the
    // replacement forward to meet it was the old answer, and it was wrong for
    // this tier: a fragmented MP4 arriving down a pipe carries no index, which
    // is exactly why the gateway reports it `seekable: false`.
    //
    // Seeking it anyway produced PIPELINE_ERROR_DECODE on the audio packet at
    // precisely the seek target — 0.766259s on a stream offset 18.936 with the
    // viewer at 19.70, and the same coincidence on three other videos. The
    // number in the browser's error was the number the player had just written
    // to currentTime.
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
    const front = visible(videos)
    front.currentTime = 45
    await act(async () => {
      fireEvent.play(front)
      // The player learns where the viewer is from timeupdate, and the next mark
      // is measured from that. Without it the reopen would compute a mark from a
      // stale position — which is the same fault, one turn later.
      fireEvent.timeUpdate(front)
      fireEvent.loadedMetadata(hidden(videos))
    })
    await settle()

    // The low rendition keeps playing — nothing the viewer sees changes.
    expect(visible(videos).src).toContain(INSTANT_URL)

    // And a fresh mark is asked for, ahead of where the viewer now is, because
    // being late says the *mark* was wrong rather than that the tier is bad.
    // Counting lateness against the tier was the trap: preparation takes about
    // as long as the lead allows, so on a long video every climb landed late,
    // three late climbs switched 1080p off for the rest of the video, and
    // pinning it by hand was the only thing that worked.
    //
    // Not compared against the previous mark, which it may legitimately be below
    // — once preparation has been measured the lead shrinks from the opening
    // guess of twenty seconds to what it actually costs.
    await waitFor(() => expect(getRemuxStart.mock.calls.length).toBeGreaterThan(1))
    const asked = getRemuxStart.mock.calls.map((call) => call[1] as number)
    expect(asked[asked.length - 1]).toBeGreaterThan(45)
  })

  it('still gives up when the late replacement has nothing to catch up with', async () => {
    const videos = await mounted()
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))

    // Late *and* empty: there is no arrived data at the viewer's position, so
    // handing over would stall on a blank frame. Keeping the low rendition is
    // the better of the two.
    Object.defineProperty(hidden(videos), 'buffered', {
      configurable: true,
      value: { length: 0, start: () => 0, end: () => 0 },
    })
    const front = visible(videos)
    front.currentTime = 45
    await act(async () => {
      fireEvent.play(front)
      fireEvent.loadedMetadata(hidden(videos))
    })
    await settle()

    expect(visible(videos).src).toContain(INSTANT_URL)
  })

  it('tries again straight away when the failed climb belonged to a seek', async () => {
    const videos = await mounted()
    await climbToRemux(videos)

    getRemuxStart.mockClear()
    await seekTo(600)
    await loadHiddenLayer(videos)

    // The seek's climb is prepared and will not load.
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
    await act(async () => {
      fireEvent.error(hidden(videos))
    })
    await settle()

    // Tried again, at once. This is the free attempt: the failure said nothing
    // about the connection, so it neither counts against the tier nor leaves the
    // viewer parked on 360p waiting for something else to happen.
    //
    // Asserted as a rule rather than as two numbers. The lead is no longer a
    // constant — it is built from how long the previous mux actually took — so
    // the exact mark depends on a measurement, and pinning it here would make
    // this test a statement about jsdom's clock. What matters is that a second
    // attempt is made and that it never asks for a mark behind the first.
    await waitFor(() => expect(getRemuxStart.mock.calls.length).toBe(2))
    const marks = getRemuxStart.mock.calls.map((call) => call[1] as number)
    expect(marks[0]).toBeGreaterThanOrEqual(600)
    expect(marks[1]).toBeGreaterThanOrEqual(marks[0])
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
  })

  it('does not try again straight away when the climb simply failed', async () => {
    const videos = await mounted()
    await waitFor(() => expect(hidden(videos).src).toContain('/remux'))
    getRemuxStart.mockClear()

    await act(async () => {
      fireEvent.error(hidden(videos))
    })
    await settle(50)

    // The contrast with the test above, and the reason the free attempt needs a
    // nudge of its own. An ordinary failure spends one of the tier's three
    // attempts and then waits: the next climb comes with the next poll of the
    // stream answer, seconds later, because a connection that just failed to
    // carry the mux will not carry it any better a moment afterwards.
    expect(getRemuxStart).not.toHaveBeenCalled()
    expect(hidden(videos).src).toBe('')
  })
})
