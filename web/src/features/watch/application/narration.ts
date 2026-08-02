/**
 * Vietnamese TTS narration overlay.
 *
 * Cues come from fetching the VTT file directly rather than from the browser's
 * TextTrack API. React adds `<track>` children through the DOM rather than in
 * static HTML, and some browsers leave the backing TextTrack uninitialised when
 * it arrives that way — readyState stays undefined whatever mode is set. Fetch
 * and parse has no such problem.
 *
 * Parsing lives in narration-vtt.ts and the timing arithmetic in
 * narration-schedule.ts, both free of the DOM and of the audio clock. What is
 * left here is the part that genuinely needs them: fetching, and putting clips
 * on the audio timeline in order.
 */
import { type CueText, parseVTT } from './narration-vtt'
import { applyAfterTranslation, prepForTranslation } from './narration-translate'
import {
  DEFAULT_SPEED,
  MAX_SPEED,
  shouldPlay,
  slotFor,
  speedFor,
  startTimeFor,
} from './narration-schedule'

const TTS_VOICE = 'Ngọc Linh'

/** How far ahead of the playhead clips are prepared. */
const PREFETCH_SEC = 10

/** Fetches allowed to be in flight at once. */
const MAX_CONCURRENT = 2

/** How many cues ahead of the one being committed are warmed in the background. */
const LOOKAHEAD_CUES = 3

/** Breath between clips, when there is room for one. */
const GAP_BETWEEN_CLIPS = 0.25

/** Narration sits above the video so the voice carries over the original audio. */
const NARRATION_GAIN = 2.5

// ---- cache ------------------------------------------------------------------
const _cache = new Map<string, AudioBuffer>()
const _active = new Map<string, Promise<AudioBuffer>>()
const _tlCache = new Map<string, string>() // EN text → VI translation

async function fetchTTS(ctx: AudioContext, text: string, speed: number): Promise<AudioBuffer> {
  // Translate EN → VI when the source subtitle is English.
  let viText = text
  if (_sourceLang === 'en') {
    const cached = _tlCache.get(text)
    if (cached) {
      viText = cached
    } else {
      const prepped = prepForTranslation(text)
      const resp = await fetch('/api/translate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: prepped, src: 'eng_Latn', tgt: 'vie_Latn' }),
      })
      if (resp.ok) {
        let translated = ((await resp.json()) as { translated: string }).translated
        if (translated) {
          translated = applyAfterTranslation(translated)
          _tlCache.set(text, translated)
          viText = translated
        }
      }
    }
  }

  const cacheKey = `${viText}@@${speed.toFixed(2)}`
  const c = _cache.get(cacheKey)
  if (c) return c
  const inflight = _active.get(cacheKey)
  if (inflight) return inflight
  while (_active.size >= MAX_CONCURRENT) {
    await Promise.race(_active.values()).catch(() => {})
  }
  const p = (async () => {
    const resp = await fetch('/api/tts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text: viText, voice: TTS_VOICE, speed }),
    })
    if (!resp.ok) throw new Error(`tts ${resp.status}`)
    const buf = await ctx.decodeAudioData(await resp.arrayBuffer())
    _cache.set(cacheKey, buf)
    return buf
  })()
  _active.set(cacheKey, p)
  try {
    return await p
  } finally {
    _active.delete(cacheKey)
  }
}

// ---- state ------------------------------------------------------------------

let _cues: CueText[] | null = null
let _cuesURL = ''
let _sourceLang = 'vi' // 'vi' = cues are Vietnamese, 'en' = needs translation

/** Index of the next cue to commit. Only ever moves forwards. */
let _cursor = 0

/** True while the pump is working; keeps it from being entered twice. */
let _pumping = false

/** Audio-clock time at which the last committed clip ends. */
let _scheduledUntil = 0

/** Clips on the timeline, so a pause or a seek can stop them. */
const _activeSources = new Set<AudioBufferSourceNode>()

let _masterGain: GainNode | null = null
let _lastTime = 0

/** Bumped whenever the timeline is abandoned, so in-flight work knows to stop. */
let _generation = 0

// ---- audio sink -------------------------------------------------------------

/**
 * Where a prepared clip goes.
 *
 * Behind a seam on purpose. Web Audio is the right tool for placing a clip at
 * an exact moment, and it is what plays narration today — but a mobile system
 * suspends it the instant the tab goes to the background, which is why video
 * audio survives there and narration does not. Reaching that will mean feeding
 * a media element instead, and when it does, only this function should have to
 * change: the ordering and timing above it are not audio-specific and should
 * not be rewritten to find that out.
 */
function scheduleBuffer(ctx: AudioContext, buffer: AudioBuffer, when: number): number {
  const source = ctx.createBufferSource()
  source.buffer = buffer

  const duration = buffer.duration
  const fade = Math.min(0.05, duration / 2)
  const gain = ctx.createGain()
  gain.gain.setValueAtTime(0, when)
  gain.gain.linearRampToValueAtTime(1, when + fade)
  gain.gain.setValueAtTime(1, Math.max(when + fade, when + duration - fade))
  gain.gain.linearRampToValueAtTime(0, when + duration)

  source.connect(gain)
  gain.connect(_masterGain!)
  source.start(when)

  _activeSources.add(source)
  source.onended = () => {
    _activeSources.delete(source)
    try {
      source.disconnect()
      gain.disconnect()
    } catch {
      /* already torn down */
    }
  }

  return when + duration
}

function stopEverything() {
  for (const source of _activeSources) {
    try {
      source.stop()
    } catch {
      /* already stopped */
    }
  }
  _activeSources.clear()
  _scheduledUntil = 0
}

// ---- loading ----------------------------------------------------------------

/**
 * Start fetching and parsing a VTT file. Safe to call repeatedly — the same URL
 * is a no-op, a different one abandons whatever was in progress.
 */
export function loadViSubtitles(url: string, lang = 'vi') {
  if (url === _cuesURL && _cues !== null) return
  _cuesURL = url
  _sourceLang = lang
  _cues = null
  _generation++
  _cursor = 0
  _pumping = false
  _lastTime = 0
  _masterGain = null
  stopEverything()

  const generation = _generation
  void fetch(url)
    .then(async (resp) => {
      if (!resp.ok) throw new Error(`VTT ${resp.status}`)
      return parseVTT(await resp.text(), lang)
    })
    .then((cues) => {
      if (generation !== _generation) return
      _cues = cues
    })
    .catch(() => {
      if (generation !== _generation) return
      // A video without narration is still a video.
      _cues = []
    })
}

// ---- scheduling -------------------------------------------------------------

/**
 * Commit cues to the timeline, one at a time, in order.
 *
 * Order is the whole of it. Every cue used to be handed to its own async fetch
 * the moment it came within range, and the guard against two clips overlapping
 * compared each arrival against "the previous clip" — meaning the previously
 * *resolved* one, which at the start of a video is whichever request happened to
 * finish first. Several seconds of speech would land on top of each other, and
 * the mess cleared up only once the backlog drained and there was one request at
 * a time to be confused about.
 *
 * So fetching and committing are separated. Fetching still overlaps, because
 * waiting for one clip before requesting the next would make narration crawl.
 * Committing does not: this loop advances a single cursor and awaits each clip
 * before placing it, so a clip can only ever be placed after the one before it.
 */
async function pump(video: HTMLVideoElement, ctx: AudioContext) {
  const cues = _cues
  if (!cues) return
  const generation = _generation

  while (_cursor < cues.length) {
    if (generation !== _generation || video.paused) return

    const index = _cursor
    const cue = cues[index]

    // Too far ahead to be worth preparing yet.
    if (cue.start - video.currentTime > PREFETCH_SEC) return

    _cursor++

    // Already spoken past, or already under way — the moment has gone.
    if (cue.start < video.currentTime) continue

    // Warm the next few in the background. They land in the cache, so when the
    // cursor reaches them there is nothing left to wait for. Deliberately not
    // awaited: this is the concurrency, and it is safe precisely because it
    // cannot commit anything.
    for (let k = index + 1; k <= index + LOOKAHEAD_CUES && k < cues.length; k++) {
      void fetchTTS(ctx, cues[k].text, DEFAULT_SPEED).catch(() => undefined)
    }

    const slot = slotFor(cues, index)

    let buffer: AudioBuffer
    try {
      buffer = await fetchTTS(ctx, cue.text, DEFAULT_SPEED)

      // Two-pass fit. The first pass is already at DEFAULT_SPEED, so the
      // natural length has to be recovered before working out what would make
      // it fit; asking for a rate against the sped-up length would under-read
      // the amount of hurry needed.
      if (buffer.duration > slot) {
        const natural = buffer.duration * DEFAULT_SPEED
        const needed = speedFor(natural, slot)
        if (needed > DEFAULT_SPEED + 0.02 && needed <= MAX_SPEED) {
          buffer = await fetchTTS(ctx, cue.text, needed)
        }
      }
    } catch {
      continue // No narration for this line; the rest carries on.
    }

    if (generation !== _generation || video.paused) return

    // The clip is ready — but is it still due? Anything that arrived after its
    // own moment is dropped rather than played late, because a late clip talks
    // over the line after it and pushes everything behind it further out. This
    // is what makes a fixed head start unnecessary: narration simply begins at
    // the first line the machine was quick enough for.
    if (!shouldPlay(cue.start, video.currentTime)) continue

    if (ctx.state === 'suspended') void ctx.resume()

    let when = startTimeFor(cue.start, video.currentTime, ctx.currentTime)
    // Never on top of the clip before it. With commits ordered, _scheduledUntil
    // really is the previous clip's end, so this is a fact rather than a guess.
    if (when < _scheduledUntil + GAP_BETWEEN_CLIPS) {
      when = _scheduledUntil + GAP_BETWEEN_CLIPS
    }

    _scheduledUntil = scheduleBuffer(ctx, buffer, when)
  }
}

/**
 * Called on a timer while narration is on. Keeps the pump fed, follows the
 * viewer's volume, and reacts to pauses and seeks.
 */
export function tickNarration(video: HTMLVideoElement, ctx: AudioContext) {
  const cues = _cues
  const now = video.currentTime

  if (video.paused) {
    stopEverything()
    _lastTime = now
    return
  }

  if (!_masterGain) {
    _masterGain = ctx.createGain()
    _masterGain.connect(ctx.destination)
  }
  _masterGain.gain.setValueAtTime(video.volume * NARRATION_GAIN, ctx.currentTime)

  // A jump in either direction abandons the timeline: everything scheduled was
  // placed against a playhead that no longer exists.
  if (Math.abs(now - _lastTime) > 0.5) {
    _generation++
    stopEverything()
    _pumping = false
    _cursor = cues ? firstCueAtOrAfter(cues, now) : 0
  }
  _lastTime = now

  if (!cues || cues.length === 0 || _pumping) return

  _pumping = true
  void pump(video, ctx).finally(() => {
    _pumping = false
  })
}

/** The first cue that has not started yet at `time`. */
function firstCueAtOrAfter(cues: CueText[], time: number): number {
  let lo = 0
  let hi = cues.length
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (cues[mid].start < time) lo = mid + 1
    else hi = mid
  }
  return lo
}

export function resetNarration() {
  _generation++
  _cues = null
  _cuesURL = ''
  _cursor = 0
  _pumping = false
  _lastTime = 0
  _masterGain = null
  stopEverything()
}

export function isNarrationActive(): boolean {
  return _cues !== null && _cues.length > 0
}

/** Whether the video carries a Vietnamese caption track. */
export function hasVietnameseSubs(video: HTMLVideoElement): boolean {
  for (let i = 0; i < video.textTracks.length; i++) {
    const lang = video.textTracks[i].language
    if (lang === 'vi' || lang === 'vie') return true
  }
  return false
}
