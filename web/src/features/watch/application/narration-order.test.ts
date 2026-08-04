import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  bindNarration,
  cancelTranslationPass,
  loadViSubtitles,
  narrationProgress,
  resetNarration,
  startTranslationPass,
  tickNarration,
} from './narration'
import { setCachePartition } from '@/features/watch/infrastructure/narration-cache'

/**
 * Clips reach the timeline in cue order, whatever order they arrive in.
 *
 * This is the overlapping-audio fault written down. Every cue within range used
 * to be handed to its own fetch the moment the tick saw it, and the guard
 * against two clips colliding compared each arrival against "the previous clip"
 * — meaning the previously *resolved* one, which at the start of a video is
 * whichever request happened to come back first. Several sentences would be
 * placed over each other, and it only settled once the backlog drained and
 * there was one request in flight to be confused about.
 *
 * So the test makes the requests finish backwards: the third cue's audio is
 * ready long before the first. Nothing about the result should change.
 */

const VTT = [
  'WEBVTT',
  '',
  '00:00:01.000 --> 00:00:02.000',
  'first line here.',
  '',
  '00:00:03.000 --> 00:00:04.000',
  'second line here.',
  '',
  '00:00:05.000 --> 00:00:06.000',
  'third line here.',
  '',
].join('\n')

/** Every start(when) that reached the audio timeline, in the order it happened. */
let scheduled: { when: number; duration: number; line: number }[] = []

/** Every source handed to the timeline, so a test can watch them be stopped. */
let sources: { stop: () => void }[] = []

const CLIP_SECONDS = 0.5

const LINES = ['first line here.', 'second line here.', 'third line here.']

/** Milliseconds each phrase takes to come back — deliberately backwards. */
const DELAYS = [60, 30, 1]

function fakeAudioContext() {
  const gain = () => ({
    gain: { setValueAtTime: () => {}, linearRampToValueAtTime: () => {} },
    connect: () => {},
    disconnect: () => {},
  })

  return {
    currentTime: 100,
    state: 'running' as const,
    destination: {},
    createGain: gain,
    // The narration runs through a limiter, because its gain is above one and
    // would otherwise flatten the peaks of speech that is already near full
    // scale. Nothing here asserts on it; it just has to exist to be connected.
    createDynamicsCompressor: () => ({
      threshold: { setValueAtTime: () => {} },
      knee: { setValueAtTime: () => {} },
      ratio: { setValueAtTime: () => {} },
      attack: { setValueAtTime: () => {} },
      release: { setValueAtTime: () => {} },
      connect: () => {},
      disconnect: () => {},
    }),
    createBufferSource() {
      const source = {
        buffer: null as { duration: number; line: number } | null,
        connect: () => {},
        disconnect: () => {},
        stop: () => {},
        onended: null as (() => void) | null,
        start(when: number) {
          scheduled.push({
            when,
            duration: source.buffer?.duration ?? 0,
            line: source.buffer?.line ?? -1,
          })
        },
      }
      sources.push(source)
      return source
    },
    // The response carries which line it is, so the test can see not just that
    // clips are spread apart but which clip landed where.
    decodeAudioData: async (ab: ArrayBuffer) => ({
      duration: CLIP_SECONDS,
      line: new Uint8Array(ab)[0],
    }),
    resume: () => {},
  }
}

function fakeVideo(currentTime: number) {
  return { currentTime, paused: false, volume: 1 }
}

/** A video the narration can also listen to, for the pause and seek handlers. */
function listeningVideo(currentTime: number) {
  const el = new EventTarget() as EventTarget & {
    currentTime: number
    paused: boolean
    volume: number
  }
  el.currentTime = currentTime
  el.paused = false
  el.volume = 1
  return el
}

beforeEach(() => {
  scheduled = []
  sources = []
  resetNarration()

  vi.stubGlobal('fetch', async (url: string, init?: { body?: string }) => {
    if (url === '/subs.vtt') {
      return { ok: true, text: async () => VTT }
    }
    if (url === '/api/tts') {
      const { text } = JSON.parse(init?.body ?? '{}') as { text: string }
      const line = LINES.indexOf(text)
      await new Promise((r) => setTimeout(r, DELAYS[line] ?? 0))
      return { ok: true, arrayBuffer: async () => new Uint8Array([line]).buffer }
    }
    throw new Error(`unexpected fetch ${url}`)
  })
})

afterEach(() => {
  resetNarration()
  vi.unstubAllGlobals()
})

async function settle(ms = 400) {
  await new Promise((r) => setTimeout(r, ms))
}

describe('a context that has not started', () => {
  // The bug: Read aloud is remembered across page loads, so the context is
  // built during render with no gesture behind it and the browser starts it
  // suspended. Its clock is frozen at zero, and clips scheduled against a
  // frozen clock are placed in the past and dropped — silence until the switch
  // was toggled off and on, which creates the context inside a click.
  it('schedules nothing and asks to be resumed', async () => {
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    let resumed = 0
    const ctx = { ...fakeAudioContext(), state: 'suspended', currentTime: 0 }
    ctx.resume = () => {
      resumed++
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(fakeVideo(0) as any, ctx as any)
    await settle()

    expect(resumed).toBeGreaterThan(0)
    expect(scheduled).toHaveLength(0)
  })

  it('speaks once the clock is running', async () => {
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const suspended = { ...fakeAudioContext(), state: 'suspended', currentTime: 0 }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(fakeVideo(0) as any, suspended as any)
    await settle()

    const ctx = fakeAudioContext()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(fakeVideo(0) as any, ctx as any)
    await settle()

    expect(scheduled.map((s) => s.line)).toEqual([0, 1, 2])
  })
})

describe('scheduling order', () => {
  it('places clips in cue order even when the audio arrives backwards', async () => {
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const ctx = fakeAudioContext()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(fakeVideo(0) as any, ctx as any)
    await settle()

    expect(scheduled).toHaveLength(3)
    // The third phrase was ready in 1ms and the first took 60ms. Which clip
    // took which slot is the question — asserting only that the slots are
    // spread apart would pass even with them handed out backwards, because the
    // no-overlap clamp spreads them either way.
    expect(scheduled.map((s) => s.line)).toEqual([0, 1, 2])
  })

  it('never places two clips over the same moment', async () => {
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const ctx = fakeAudioContext()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(fakeVideo(0) as any, ctx as any)
    await settle()

    for (let i = 1; i < scheduled.length; i++) {
      const previousEnd = scheduled[i - 1].when + scheduled[i - 1].duration
      expect(scheduled[i].when).toBeGreaterThanOrEqual(previousEnd)
    }
  })

  it('speaks each line once', async () => {
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const ctx = fakeAudioContext()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const video = fakeVideo(0) as any
    // Ticking repeatedly is what the player does; it must not re-commit cues.
    tickNarration(video, ctx as any)
    tickNarration(video, ctx as any)
    await settle()
    tickNarration(video, ctx as any)
    await settle()

    expect(scheduled).toHaveLength(3)
  })

  it('drops a line whose moment passed while its audio was being made', async () => {
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const ctx = fakeAudioContext()
    // The playhead is already past every cue in the file.
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(fakeVideo(30) as any, ctx as any)
    await settle()

    // Nothing is spoken over the top of what comes later just to catch up.
    expect(scheduled).toHaveLength(0)
  })

  it('places well beyond the next few seconds', async () => {
    // The runway is what carries narration through a backgrounded tab: nothing
    // new can be placed there, because timers are throttled to a crawl or
    // frozen, but whatever is already on the audio timeline still plays.
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const ctx = fakeAudioContext()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(fakeVideo(0) as any, ctx as any)
    await settle()

    // All three cues, the last of which is five seconds out, are placed from a
    // single tick — no further wake-up needed.
    expect(scheduled).toHaveLength(3)
  })

  it('stops on the video’s own pause event, with no tick involved', async () => {
    // The case this exists for: pause pressed on a lock screen, tab in the
    // background. Polling would notice somewhere in the next minute, and until
    // then a voice narrates a video that has stopped.
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const ctx = fakeAudioContext()
    const video = document.createElement('video')
    Object.defineProperty(video, 'paused', { configurable: true, value: false })
    Object.defineProperty(video, 'currentTime', { configurable: true, value: 0, writable: true })

    const unbind = bindNarration(video)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(video as any, ctx as any)
    await settle()
    expect(scheduled.length).toBeGreaterThan(0)

    const stops: number[] = []
    for (const source of sources) source.stop = () => stops.push(1)

    // No tick after this point — only the event.
    video.dispatchEvent(new Event('pause'))
    expect(stops.length).toBeGreaterThan(0)

    unbind()
  })

  it('schedules nothing while the video is paused', async () => {
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const ctx = fakeAudioContext()
    const video = { currentTime: 0, paused: true, volume: 1 }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(video as any, ctx as any)
    await settle()

    expect(scheduled).toHaveLength(0)
  })
})

/**
 * Pressing play has to start asking for audio straight away.
 *
 * Reported as "TTS requests come a long time after play, and switching Read
 * aloud off and on fixes it" — which is the tell: that toggle tears the tick
 * loop down and rebuilds it, so whatever the loop was holding onto was the
 * problem, not the settings.
 */
describe('resuming after a pause', () => {
  it('asks for audio on the first tick after play', async () => {
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const ctx = fakeAudioContext()
    const video = listeningVideo(0)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const unbind = bindNarration(video as any)

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(video as any, ctx as any)
    await settle()
    const beforePause = scheduled.length
    expect(beforePause).toBeGreaterThan(0)

    // Pause, exactly as the element would: the flag and the event.
    video.paused = true
    video.dispatchEvent(new Event('pause'))
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(video as any, ctx as any)
    await settle(20)

    scheduled = []
    // Play again, from the top, where every cue is still ahead.
    video.paused = false
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(video as any, ctx as any)
    await settle()

    expect(scheduled.length).toBeGreaterThan(0)
    unbind()
  })

  it('does not sit out the rest of the video after a pause', async () => {
    // The cursor travels with the clips placed ahead of the playhead. Leaving
    // it out there meant play resumed from a point the video had not reached,
    // and nothing was said until it caught up.
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const ctx = fakeAudioContext()
    const video = listeningVideo(0)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const unbind = bindNarration(video as any)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(video as any, ctx as any)
    await settle()

    video.paused = true
    video.dispatchEvent(new Event('pause'))
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(video as any, ctx as any)
    await settle(20)

    scheduled = []
    video.paused = false
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(video as any, ctx as any)
    await settle()

    // All three lines are still ahead of a playhead that never moved, so all
    // three should be placed again.
    expect(scheduled.map((s) => s.line)).toEqual([0, 1, 2])
    unbind()
  })
})

describe('which element narration listens to', () => {
  it('follows the layer that is actually in front', async () => {
    // The player keeps two <video> elements and swaps them — when the download
    // replaces the upstream stream, or the quality changes. Listeners bound
    // once stay on the element that was in front at the time, so a pause on the
    // one actually showing goes unheard.
    loadViSubtitles('/subs.vtt', 'vi')
    await settle(20)

    const first = listeningVideo(0)
    const second = listeningVideo(0)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const unbindFirst = bindNarration(first as any)

    const ctx = fakeAudioContext()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(first as any, ctx as any)
    await settle()
    expect(scheduled.length).toBeGreaterThan(0)

    // The swap: stop listening to the old layer, start on the new one.
    unbindFirst()
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const unbindSecond = bindNarration(second as any)

    // A pause on the element now in front has to be heard.
    second.paused = true
    second.dispatchEvent(new Event('pause'))
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(second as any, ctx as any)
    await settle(20)

    scheduled = []
    second.paused = false
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    tickNarration(second as any, ctx as any)
    await settle()

    expect(scheduled.map((s) => s.line)).toEqual([0, 1, 2])
    unbindSecond()
  })
})

describe('a batch that fails', () => {
  it('is asked for again rather than lost', async () => {
    // A failed batch used to end those cues for the whole pass: the loop moved
    // on and never came back, so one blip left a permanent hole.
    let batchCalls = 0
    vi.stubGlobal('fetch', async (url: string, init?: { body?: string }) => {
      if (url === '/subs.vtt') return { ok: true, text: async () => VTT }
      if (url === '/api/translate/batch') {
        batchCalls++
        // Down for the first two tries, then back.
        if (batchCalls <= 2) throw new Error('Failed to fetch')
        const { cues } = JSON.parse(init?.body ?? '{}') as { cues: string[] }
        return { ok: true, json: async () => ({ translations: cues.map((c) => 'VI ' + c) }) }
      }
      if (url.startsWith('/api/videos/')) {
        return { ok: true, json: async () => ({ entries: {} }) }
      }
      if (url === '/api/tts') {
        return { ok: true, arrayBuffer: async () => new Uint8Array([0]).buffer }
      }
      throw new Error(`unexpected fetch ${url}`)
    })

    setCachePartition('m1')
    loadViSubtitles('/subs.vtt', 'en')
    startTranslationPass('vid1', 0)

    // Long enough for the two failures and their backoff.
    await settle(5000)

    expect(batchCalls).toBeGreaterThanOrEqual(3)
    expect(narrationProgress().done).toBeGreaterThan(0)
    cancelTranslationPass()
  }, 20_000)

  it('stops rather than argue with a translator that is down', async () => {
    // Three failed batches in a row is not bad luck, and a video's worth of
    // retries against something that is down is the mistake CLAUDE.md §8
    // records: pushing at a block makes it last longer.
    let batchCalls = 0
    vi.stubGlobal('fetch', async (url: string) => {
      if (url === '/subs.vtt') return { ok: true, text: async () => VTT }
      if (url === '/api/translate/batch') {
        batchCalls++
        throw new Error('Failed to fetch')
      }
      if (url.startsWith('/api/videos/')) return { ok: true, json: async () => ({ entries: {} }) }
      throw new Error(`unexpected fetch ${url}`)
    })

    setCachePartition('m1')
    loadViSubtitles('/subs.vtt', 'en')
    startTranslationPass('vid1', 0)
    await settle(12_000)

    // Three attempts per batch, three batches, and then it gives up — not one
    // attempt for every cue in the video.
    expect(batchCalls).toBeLessThanOrEqual(9)
    expect(narrationProgress().running).toBe(false)
    cancelTranslationPass()
  }, 30_000)
})
