import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { loadViSubtitles, resetNarration, tickNarration } from './narration'

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

beforeEach(() => {
  scheduled = []
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
