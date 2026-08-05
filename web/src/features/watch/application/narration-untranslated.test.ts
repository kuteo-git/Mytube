import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  cancelTranslationPass,
  loadViSubtitles,
  narrationCursor,
  resetNarration,
  startTranslationPass,
  tickNarration,
} from './narration'
import { setCachePartition } from '@/features/watch/infrastructure/narration-cache'

/**
 * A line the translator has not reached is waited for, not lost.
 *
 * The reported fault, and the reason it only ever showed itself on a video that
 * had just been added: the pump moved its cursor past a cue before asking for
 * the audio, and the cursor only ever goes forwards. On a video already on disk
 * the translations are cached and there is nothing to wait for, so it worked.
 * On a new one the first lines were not translated yet, the cursor stepped over
 * them, and narration stayed silent for the rest of the video — unless the
 * viewer seeked, which is the one thing that puts the cursor back. That is
 * exactly what people were doing to "wake it up".
 */

const VTT = [
  'WEBVTT',
  '',
  '00:00:10.000 --> 00:00:12.000',
  'first line here.',
  '',
  '00:00:14.000 --> 00:00:16.000',
  'second line here.',
  '',
].join('\n')

/** How long the translator takes to answer, so a tick can land before it does. */
let translateDelayMs = 0
/** Whether it answers at all. */
let translatorAnswers = true

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
    createDynamicsCompressor: () => ({
      threshold: { setValueAtTime: () => {} },
      knee: { setValueAtTime: () => {} },
      ratio: { setValueAtTime: () => {} },
      attack: { setValueAtTime: () => {} },
      release: { setValueAtTime: () => {} },
      connect: () => {},
      disconnect: () => {},
    }),
    createBufferSource: () => ({
      buffer: null as unknown,
      connect: () => {},
      disconnect: () => {},
      stop: () => {},
      onended: null as (() => void) | null,
      start: () => {},
    }),
    decodeAudioData: async () => ({ duration: 0.5 }),
    resume: () => {},
  }
}

const video = (currentTime: number) => ({ currentTime, paused: false, volume: 1 })

const settle = (ms = 50) => new Promise((r) => setTimeout(r, ms))

beforeEach(() => {
  translateDelayMs = 0
  translatorAnswers = true
  resetNarration()
  cancelTranslationPass()
  setCachePartition('m1')

  vi.stubGlobal('fetch', async (url: string, init?: { body?: string }) => {
    if (url === '/subs.vtt') return { ok: true, text: async () => VTT }
    if (url === '/api/tts') {
      return { ok: true, arrayBuffer: async () => new Uint8Array([0]).buffer }
    }
    if (url.includes('/translate/batch')) {
      if (translateDelayMs) await new Promise((r) => setTimeout(r, translateDelayMs))
      const { cues } = JSON.parse(init?.body ?? '{}') as { cues: string[] }
      return {
        ok: true,
        json: async () => ({
          translations: translatorAnswers ? cues.map((c) => 'VI:' + c) : cues.map(() => ''),
        }),
      }
    }
    // The narration cache, the cue store and the written subtitle file.
    return { ok: true, json: async () => ({ entries: {} }) }
  })
})

afterEach(() => {
  cancelTranslationPass()
  resetNarration()
  vi.unstubAllGlobals()
})

/**
 * English cues, which is the case that needs translating — and the only case
 * this gate applies to. A video whose subtitles are already Vietnamese has
 * nothing to wait for.
 */
async function englishCues() {
  loadViSubtitles('/subs.vtt', 'en')
  startTranslationPass('abc', 0)
  await settle()
}

describe('a cue the translator has not reached', () => {
  it('does not take the cursor past it', async () => {
    // The cursor stopping where it is means the next tick — a tenth of a second
    // — asks again. Moving on is permanent, and permanent is what made a whole
    // video silent.
    translatorAnswers = false
    await englishCues()

    const ctx = fakeAudioContext() as unknown as AudioContext
    tickNarration(video(9) as unknown as HTMLVideoElement, ctx)
    await settle()

    expect(narrationCursor()).toBe(0)
  })

  it('is spoken once the translation arrives', async () => {
    // The translator is slow enough that the first tick lands before it has
    // answered — which is exactly the shape of a video just added.
    translateDelayMs = 250
    loadViSubtitles('/subs.vtt', 'en')
    startTranslationPass('abc', 0)
    await settle(20)

    const ctx = fakeAudioContext() as unknown as AudioContext
    const el = video(9) as unknown as HTMLVideoElement

    tickNarration(el, ctx)
    await settle(50)
    expect(narrationCursor()).toBe(0)

    // The pass catches up, and the very next tick picks the line up again.
    await settle(500)
    tickNarration(el, ctx)
    await settle(50)

    expect(narrationCursor()).toBeGreaterThan(0)
  })

  it('gives up on it once its moment has gone', async () => {
    // Waiting must not become stuck. A line whose moment has passed can no
    // longer be said in the right place, so the cursor moves on and the rest of
    // the video carries on — the same trade the scheduler already makes for a
    // clip that arrives late.
    translatorAnswers = false
    await englishCues()

    const ctx = fakeAudioContext() as unknown as AudioContext
    // Well past the first cue, which starts at ten seconds.
    tickNarration(video(13) as unknown as HTMLVideoElement, ctx)
    await settle()

    expect(narrationCursor()).toBeGreaterThan(0)
  })
})
