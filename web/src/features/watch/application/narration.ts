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
import { CONTEXT_CUES, planBatches, translateBatch } from './narration-batch'
import { toVTT } from './narration-vtt-write'
import {
  hashCue,
  loadNarrationCache,
  saveNarrationCache,
  saveNarrationCues,
  saveNarrationVtt,
  type NarrationEngine,
} from '@/features/watch/infrastructure/narration-cache'
import {
  DEFAULT_SPEED,
  MAX_SPEED,
  shouldPlay,
  slotFor,
  speedFor,
  startTimeFor,
} from './narration-schedule'

const TTS_VOICE = 'Ngọc Linh'

/**
 * How far ahead of the playhead clips are prepared and placed.
 *
 * A minute, not the ten seconds it was, and the reason is background playback
 * on a phone. `start(when)` is honoured by the audio thread whether or not any
 * JavaScript is running, so whatever has been placed will play; but nothing new
 * gets placed while the tab is in the background, because timers there are
 * throttled to a crawl and often frozen outright. Ten seconds of runway meant
 * narration went quiet ten seconds after the screen went off, while the video's
 * own audio — a media element, driven by the browser rather than by us — kept
 * going. Sixty seconds of runway is sixty seconds of narration that no longer
 * depends on being woken up.
 *
 * It does not help iOS, which suspends the audio clock itself when the page goes
 * to the background — so there is no clock left to honour what was placed.
 * Routing through a MediaStream and an `<audio>` element was tried, on the
 * theory that a media element is what a phone keeps alive; Safari sent it down
 * its real-time communication path instead and returned a rasp that cut out
 * every second. See CLAUDE.md §8.3b.
 */
const PREFETCH_SEC = 60

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
/** Cue text -> Vietnamese, for the engine currently selected. */
const _tlCache = new Map<string, string>()

let _engine: NarrationEngine = 'qwen'
let _passVideoId = ''
let _passRunning = false
let _passGeneration = 0
let _passTotal = 0
let _passDone = 0
let _passPhase: NarrationPhase = 'idle'
/** Written since the last flush, so a flush posts only what is new. */
const _unsaved = new Map<string, string>()

export function setNarrationEngine(engine: NarrationEngine) {
  if (engine === _engine) return
  // Each engine has its own answers; keeping the old ones would mix the two
  // and make the comparison this menu exists for meaningless.
  _engine = engine
  _tlCache.clear()
  _unsaved.clear()
  _passGeneration++
  _passRunning = false
  _passTotal = 0
  _passDone = 0
  _passPhase = 'idle'
}

/**
 * Which video narration is for, so synthesised clips can be filed beside it.
 *
 * Set independently of the translation pass: the realtime engine has no pass at
 * all, and its clips are just as worth keeping.
 */
export function setNarrationVideo(videoId: string) {
  _passVideoId = videoId
}

export function translatedCue(text: string): string | undefined {
  return _tlCache.get(text)
}

/**
 * Where the background pass has got to.
 *
 * The phase matters as much as the numbers. A count of zero is true of a pass
 * that has not started, one waiting on a subtitle file, one whose subtitles
 * never arrived, and one that has nothing to do because the cues are already
 * Vietnamese. Reporting all four as "preparing" — which the first version of
 * this did — tells the viewer nothing and leaves no way to tell a slow pass
 * from a broken one.
 */
export type NarrationPhase =
  | 'idle'
  | 'waiting-subtitles'
  | 'no-subtitles'
  | 'not-needed'
  | 'translating'
  | 'done'

export function narrationProgress(): {
  done: number
  total: number
  running: boolean
  phase: NarrationPhase
} {
  return {
    done: _passDone,
    total: _passTotal,
    running: _passRunning,
    phase: _passPhase,
  }
}

/** The cue covering `time`, or null when nothing is being said. */
export function currentCueText(cues: CueText[], time: number): string | null {
  for (const c of cues) {
    if (time >= c.start && time < c.end) return c.text
  }
  return null
}

/** The parsed cues, for callers that render rather than speak. */
export function narrationCues(): CueText[] {
  return _cues ?? []
}

/** Resolved when the cue list lands, by whoever is loading it. */
let _cuesWaiters: Array<(cues: CueText[]) => void> = []

function announceCues(cues: CueText[]) {
  const waiters = _cuesWaiters
  _cuesWaiters = []
  waiters.forEach((w) => w(cues))
}

/**
 * The cues, once there are some.
 *
 * The translation pass used to poll `_cues` every 100ms and give up after ten
 * seconds, which turned "the subtitle file is still downloading" into a
 * permanent "no subtitles" that never retried — reported against a video whose
 * VTT was sitting on disk and served a 200 the whole time. Waiting on the load
 * itself has no deadline to get wrong.
 */
export function whenCuesReady(): Promise<CueText[]> {
  if (_cues !== null) return Promise.resolve(_cues)
  return new Promise((resolve) => _cuesWaiters.push(resolve))
}

/** Index of the cue playing at `time`, or the next one due. */
export function nearestCueIndex(cues: CueText[], time: number): number {
  if (cues.length === 0) return 0
  for (let i = 0; i < cues.length; i++) {
    if (cues[i].end > time) return i
  }
  return cues.length - 1
}

/**
 * Translate forward from the playhead until the video runs out.
 *
 * Not from cue zero: a viewer resuming at minute thirty would otherwise wait
 * while the opening is translated. Not one cue at a time either — a batch
 * carries the preceding lines as context, which is what keeps pronouns and
 * terminology steady across a video, and is the whole reason for using a model
 * that can read more than one sentence.
 *
 * Runs ahead of playback by roughly two to four times on this machine, so it
 * only has to keep going, not hurry.
 */
export function startTranslationPass(videoId: string, fromTime: number) {
  if (_passRunning && videoId === _passVideoId) return
  _passVideoId = videoId
  _passRunning = true
  _passGeneration++
  _passTotal = 0
  _passDone = 0
  _passPhase = 'waiting-subtitles'
  const generation = _passGeneration

  void (async () => {
    // `finally`, not a flag cleared at the end: every guard below returns early
    // when a newer pass has superseded this one, and each of those returns would
    // otherwise leave `_passRunning` true forever — which is the one state that
    // stops a pass from ever being started again.
    try {
      const byHash = await loadNarrationCache(videoId, _engine)
      if (generation !== _passGeneration) return

      // Wait for the cue list, which loadViSubtitles is fetching in parallel.
      const cues = await whenCuesReady()
      if (generation !== _passGeneration) return
      if (cues.length === 0) {
        // The load finished and produced nothing — a missing or unparseable
        // VTT. Distinct from still waiting, which no longer expires.
        _passPhase = 'no-subtitles'
        return
      }
      if (_sourceLang !== 'en') {
        // Already Vietnamese. Narration reads the cues as they are.
        _passPhase = 'not-needed'
        return
      }

      const first = nearestCueIndex(cues, fromTime)
      const texts = cues.slice(first).map((c) => c.text)
      const hashes = await Promise.all(texts.map(hashCue))
      if (generation !== _passGeneration) return

      // Seed from disk. The cache is keyed by hash, the speaking path looks up
      // by text, so the two are joined here rather than at every lookup.
      texts.forEach((text, i) => {
        const hit = byHash.get(hashes[i])
        if (hit) _tlCache.set(text, hit)
      })
      const countDone = () => texts.filter((t) => _tlCache.has(t)).length
      _passTotal = texts.length
      _passDone = countDone()
      _passPhase = _passDone >= _passTotal ? 'done' : 'translating'

      for (const { start, end } of planBatches(texts.length)) {
        if (generation !== _passGeneration) return

        const slice = texts.slice(start, end)
        const missing = slice.filter((t) => !_tlCache.has(t))
        if (missing.length === 0) continue

        const context = texts.slice(Math.max(0, start - CONTEXT_CUES), start)
        const out = await translateBatch(missing, context, _engine)
        if (generation !== _passGeneration) return

        missing.forEach((text, i) => {
          const vi = out[i]
          if (!vi) return
          _tlCache.set(text, vi)
          const at = texts.indexOf(text)
          if (at >= 0) _unsaved.set(hashes[at], vi)
        })
        _passDone = countDone()

        // Flush as we go. A viewer who closes the tab halfway keeps the half
        // that was paid for.
        const batchSaved = new Map(_unsaved)
        _unsaved.clear()
        void saveNarrationCache(videoId, _engine, batchSaved)
      }

      // Only now, with the whole pass behind it. Written mid-pass this would be
      // a subtitle file that looks complete and stops halfway through the
      // video — worse than not being there.
      if (generation === _passGeneration) {
        void saveNarrationVtt(videoId, toVTT(cues, _tlCache))
      }
    } finally {
      if (generation === _passGeneration) {
        _passRunning = false
        if (_passPhase === 'translating' || _passPhase === 'waiting-subtitles') {
          _passPhase = _passTotal > 0 && _passDone >= _passTotal ? 'done' : 'idle'
        }
      }
    }
  })()
}

async function fetchTTS(ctx: AudioContext, text: string, speed: number): Promise<AudioBuffer> {
  // Translate EN → VI when the source subtitle is English.
  let viText = text
  if (_sourceLang === 'en') {
    const cached = _tlCache.get(text)
    if (cached) {
      viText = cached
    } else if (_engine === 'nllb') {
      // The realtime engine still translates on demand: it is the arm of the
      // comparison that has no background pass, and taking that away would
      // leave nothing to compare against.
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
          const vi = translated
          void hashCue(text).then((h) =>
            saveNarrationCache(_passVideoId, 'nllb', new Map([[h, vi]])),
          )
          viText = translated
        }
      }
    }
    // Under the batch engine an untranslated cue means the pass has not reached
    // it yet. Speaking the English would be worse than saying nothing, so the
    // cue is skipped and the loop carries on.
    if (viText === text && _engine === 'qwen') {
      throw new Error('not translated yet')
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
      // videoId tells the gateway where to keep the clip. Synthesis is the
      // expensive half of narration and its bytes never change for the same
      // words at the same tempo, so it belongs on disk beside the video.
      body: JSON.stringify({
        text: viText,
        voice: TTS_VOICE,
        speed,
        videoId: _passVideoId,
      }),
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
 * Connect the narration to the speakers, through something that will not let it
 * clip.
 *
 * The gain below is well above one, so that the voice carries over the film's
 * own audio. Fed straight to the destination that is a guarantee of distortion:
 * anything louder than 1/gain gets its peaks flattened, and synthesised speech
 * arrives close to full scale, so it is most of it. A limiter keeps the loudness
 * and takes away the clipping — the crackle was not a mystery, it was arithmetic.
 *
 * It used to go through a MediaStream into an `<audio>` element, on the theory
 * that a phone keeps media elements alive in the background where it suspends
 * Web Audio. On iOS that made things worse rather than better: Safari puts a
 * MediaStream through its real-time communication path, which resampled the
 * speech into a rasp and cut each clip off after about a second. The theory was
 * worth an hour to test and the test answered it.
 */
function connectOutput(ctx: AudioContext, node: GainNode) {
  const limiter = ctx.createDynamicsCompressor()
  // Ratio and knee chosen to catch peaks rather than to squash the whole
  // signal: below the threshold nothing is touched at all.
  limiter.threshold.setValueAtTime(-3, ctx.currentTime)
  limiter.knee.setValueAtTime(0, ctx.currentTime)
  limiter.ratio.setValueAtTime(20, ctx.currentTime)
  limiter.attack.setValueAtTime(0.003, ctx.currentTime)
  limiter.release.setValueAtTime(0.15, ctx.currentTime)

  node.connect(limiter)
  limiter.connect(ctx.destination)
}

/**
 * Where a prepared clip goes.
 *
 * Behind a seam on purpose. Web Audio is the right tool for placing a clip at an
 * exact moment and stays; what may yet have to change is where its output ends
 * up, and that is `connectOutput` above rather than anything here. The ordering
 * and timing this sits under are not audio-specific and should not have to be
 * rewritten to find that out.
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
      announceCues(cues)
      void saveNarrationCues(_passVideoId, cues)
    })
    .catch(() => {
      if (generation !== _generation) return
      // A video without narration is still a video.
      _cues = []
      announceCues([])
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
 * Listen to the video rather than ask it.
 *
 * Placing a minute of narration ahead only works if stopping it does not depend
 * on a timer, because the moment that matters most is the one where timers are
 * least reliable: the viewer presses pause on a lock screen, with the tab in the
 * background. Polling would notice somewhere in the next minute, and until then
 * a voice would carry on narrating a video that had stopped. These events fire
 * from the browser whether or not anything of ours is being woken up.
 *
 * Returns a function that removes the listeners.
 */
export function bindNarration(video: HTMLVideoElement): () => void {
  const onPause = () => stopEverything()

  const onSeeking = () => {
    // Everything placed was placed against a playhead that no longer exists.
    _generation++
    stopEverything()
    _pumping = false
    _cursor = _cues ? firstCueAtOrAfter(_cues, video.currentTime) : 0
    _lastTime = video.currentTime
  }

  video.addEventListener('pause', onPause)
  video.addEventListener('ended', onPause)
  video.addEventListener('seeking', onSeeking)

  return () => {
    video.removeEventListener('pause', onPause)
    video.removeEventListener('ended', onPause)
    video.removeEventListener('seeking', onSeeking)
  }
}

/**
 * Called on a timer while narration is on. Keeps the pump fed, follows the
 * viewer's volume, and catches a seek the events missed.
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
    connectOutput(ctx, _masterGain)
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
  // Cancel any translation pass and release anything waiting on cues. A waiter
  // left hanging would hold _passRunning true, and that flag being stuck is the
  // one state from which no pass can ever start again.
  _passGeneration++
  _passRunning = false
  _passPhase = 'idle'
  announceCues([])
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
