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
import {
  CONTEXT_CUES,
  lastBatchError,
  planBatches,
  translateBatch,
  workOrder,
} from './narration-batch'
import {
  BATCH_ATTEMPTS,
  GIVE_UP_AFTER,
  retryDelayMs,
  worthRetrying,
} from './narration-retry'
import { createPregen } from './narration-pregen'
import { toVTT } from './narration-vtt-write'
import { estimateEtaSeconds } from './narration-eta'
import {
  hashCue,
  loadNarrationCache,
  saveNarrationCache,
  saveNarrationCues,
  saveNarrationVtt,
  whenPartitionReady,
} from '@/features/watch/infrastructure/narration-cache'
import { hasStalled } from './narration-watchdog'
import {
  shouldPlay,
  slotFor,
  startTimeFor,
  scheduleAt,
  tooLateToPlay,
} from './narration-schedule'

/** Whichever voice the viewer picked. The default is what shipped before. */
let _voice = 'Ngọc Linh'

export function setNarrationVoice(voice: string) {
  if (voice) _voice = voice
}

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

/**
 * Gain on the narration bus, set by the player from the viewer's settings.
 *
 * It used to be a constant multiplied by `video.volume` — which is the ducked
 * figure, so the two levels could not be moved independently. The player now
 * works out both from levelsFor() and hands this one over.
 */
let _narrationGain = 0.5

export function setNarrationGain(gain: number) {
  _narrationGain = gain
}

// ---- cache ------------------------------------------------------------------
const _cache = new Map<string, AudioBuffer>()
const _active = new Map<string, Promise<AudioBuffer>>()
/** Cue text -> Vietnamese, for the engine currently selected. */
const _tlCache = new Map<string, string>()

let _passVideoId = ''
let _passRunning = false
let _passGeneration = 0
let _passTotal = 0
let _passDone = 0
let _passPhase: NarrationPhase = 'idle'
/** The message of anything the pass threw, for the status line. */
let _passThrew = ''
/** When translating actually began, and how much was already cached by then. */
let _passAbort: AbortController | null = null
/**
 * Bumped each time the subtitle file is written.
 *
 * The file is what makes the translation a selectable track, and the track list
 * is fetched once when the video loads — so without something to watch, a
 * viewer who sat through a translation still had no VI option until they
 * reloaded the page.
 */
let _vttVersion = 0
let _passStartedAt = 0
let _passBaseline = 0

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
  // The four steps that come before the first batch, each named. They were one
  // phase called 'waiting-subtitles', which is how a pass stuck waiting for the
  // translator settings reported "Loading subtitles…" against subtitles that
  // were already on screen — and left no way to tell which step was slow.
  | 'waiting-config'
  | 'no-translator'
  | 'reading-cache'
  | 'waiting-subtitles'
  | 'hashing'
  | 'no-subtitles'
  | 'not-needed'
  | 'translating'
  | 'done'
  | 'failed'

export function narrationProgress(): {
  done: number
  total: number
  running: boolean
  phase: NarrationPhase
  /** Why the last batch produced nothing, when it did. */
  error: string
  /** Seconds of work left, or null while there is no rate worth quoting. */
  etaSeconds: number | null
  /** Increments whenever the translated subtitle file has been written. */
  vttVersion: number
} {
  return {
    done: _passDone,
    total: _passTotal,
    running: _passRunning,
    phase: _passPhase,
    vttVersion: _vttVersion,
    error: _passThrew || lastBatchError(),
    etaSeconds:
      _passPhase === 'translating'
        ? estimateEtaSeconds({
            done: _passDone,
            total: _passTotal,
            baseline: _passBaseline,
            elapsedMs: _passStartedAt ? Date.now() - _passStartedAt : 0,
          })
        : null,
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
/**
 * End the current translation pass, for good.
 *
 * Bumps the generation before releasing waiters, so a pass suspended on
 * whenCuesReady exits at its generation check rather than concluding from the
 * empty list that the video has no subtitles. Releasing them at all matters:
 * a waiter left hanging holds _passRunning true, and that flag being stuck is
 * the one state from which no pass can ever start again.
 */
export function cancelTranslationPass() {
  // Stop the work upstream, not just our interest in it. A batch nobody is
  // waiting for is still a batch the router is spending time on — the same
  // reasoning that cancels a download when you leave a video (CLAUDE.md §8b).
  _passAbort?.abort()
  _passAbort = null
  _passGeneration++
  _passRunning = false
  _passPhase = 'idle'
  _passTotal = 0
  _passDone = 0
  announceCues([])
}

export function whenCuesReady(): Promise<CueText[]> {
  if (_cues !== null) return Promise.resolve(_cues)
  // Nothing is fetching, so nothing is ever going to resolve this. Waiting here
  // was the whole of "Loading subtitles…" against a subtitle file on disk,
  // served, and listed in the menu.
  //
  // The two halves are reset by different things: `resetNarration` clears the
  // cues when the video changes, while the effect that reloads them fires on the
  // subtitle *addresses* changing. Whenever those do not coincide the cue list
  // was null with no request behind it, and this promise could not be resolved
  // by any code path — the pass held there for the life of the page.
  //
  // An empty answer is the honest one and it is not final: it puts the pass on
  // "no subtitles", and a later load announces cues for the pass after it.
  if (!_cuesLoading) return Promise.resolve([])
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
  _passPhase = 'waiting-config'
  _passThrew = ''
  _passStartedAt = 0
  _passBaseline = 0
  _passAbort = new AbortController()
  const signal = _passAbort.signal
  const generation = _passGeneration

  void (async () => {
    // `finally`, not a flag cleared at the end: every guard below returns early
    // when a newer pass has superseded this one, and each of those returns would
    // otherwise leave `_passRunning` true forever — which is the one state that
    // stops a pass from ever being started again.
    try {
      // Which model is configured decides which partition these belong in, and
      // that answer comes from the server. Reading before it lands would file
      // this video's work under the wrong model.
      const configured = await whenPartitionReady()
      if (generation !== _passGeneration) return
      if (!configured) {
        // Nothing to translate with. Saying so is the whole of what can be
        // done here: the answer lives in Settings, not in waiting longer.
        _passPhase = 'no-translator'
        return
      }

      _passPhase = 'reading-cache'
      const byHash = await loadNarrationCache(videoId)
      if (generation !== _passGeneration) return

      // Wait for the cue list, which loadViSubtitles is fetching in parallel.
      _passPhase = 'waiting-subtitles'
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
      const allTexts = cues.map((c) => c.text)
      // Hashing is the video's whole cue list through a SHA-1 written in
      // JavaScript — `crypto.subtle` does not exist outside a secure context,
      // and this is served over plain HTTP to a LAN address. On a long video it
      // is long enough to be worth its own name on the status line.
      _passPhase = 'hashing'
      const hashes = await Promise.all(allTexts.map(hashCue))
      if (generation !== _passGeneration) return

      // Seed from disk for EVERY cue, not just the ones ahead of the playhead.
      // Seeding is a lookup against a map already in hand, and restricting it
      // meant a seek backwards fell silent over cues whose translations were
      // sitting on disk the whole time.
      const hashOf = new Map(allTexts.map((t, i) => [t, hashes[i]]))
      allTexts.forEach((text, i) => {
        const hit = byHash.get(hashes[i])
        if (hit) _tlCache.set(text, hit)
      })

      // Work outward from the playhead and wrap, so the whole video is covered
      // and the viewer's next line is still first in the queue.
      const order = workOrder(allTexts.length, first)
      const texts = order.map((i) => allTexts[i])
      // Each line's time budget travels with it, in the same order. See
      // translateBatch: the translation is spoken over a video, so how long a
      // line has is part of the question being asked.
      const slots = order.map((i) => slotFor(cues, i))

      const countDone = () => allTexts.filter((t) => _tlCache.has(t)).length
      let consecutiveFailures = 0
      _passTotal = allTexts.length
      _passDone = countDone()
      _passPhase = _passDone >= _passTotal ? 'done' : 'translating'
      // The clock starts here, not when the pass was asked for: everything
      // before this was fetching a subtitle file and reading the cache, and
      // counting it would make the first estimate far too gloomy.
      _passStartedAt = Date.now()
      _passBaseline = _passDone

      for (const { start, end } of planBatches(texts.length)) {
        if (generation !== _passGeneration) return

        const slice = texts.slice(start, end)
        // The budget has to be filtered alongside the text it belongs to, or a
        // batch with any cached line in it would hand every later line the
        // wrong number.
        const missing: string[] = []
        const missingSlots: number[] = []
        slice.forEach((t, i) => {
          if (_tlCache.has(t)) return
          missing.push(t)
          missingSlots.push(slots[start + i])
        })
        if (missing.length === 0) continue

        const context = texts.slice(Math.max(0, start - CONTEXT_CUES), start)

        // Try again rather than lose the batch. A failure used to end those
        // cues for the rest of the pass — the loop moved on and never asked for
        // them again, so one blip left a hole only a settings change could
        // fill, because that restarts the pass.
        let out: string[] = []
        for (let attempt = 0; attempt < BATCH_ATTEMPTS; attempt++) {
          if (attempt > 0) {
            await new Promise((r) => setTimeout(r, retryDelayMs(attempt)))
            if (generation !== _passGeneration) return
          }
          out = await translateBatch(missing, context, signal, missingSlots)
          if (out.some(Boolean)) break
          if (!worthRetrying({ aborted: signal.aborted, error: lastBatchError() })) break
        }

        // Consecutive failures mean the translator is down, not that this batch
        // was unlucky. A video's worth of retries against something that is down
        // is a long argument nobody wins — the same lesson as the backfill that
        // hammered a rate limit and made the block last longer (CLAUDE.md §8).
        if (out.some(Boolean)) {
          consecutiveFailures = 0
        } else if (!signal.aborted) {
          consecutiveFailures++
          if (consecutiveFailures >= GIVE_UP_AFTER) return
        }

        // Bank what came back before asking whether anyone still wants it.
        // These lines were translated and paid for; throwing them away because
        // the viewer moved on in the meantime means paying again next time.
        const banked = new Map<string, string>()
        missing.forEach((text, i) => {
          const vi = out[i]
          if (!vi) return
          const h = hashOf.get(text)
          if (h) banked.set(h, vi)
          // The in-memory cache belongs to whichever pass is current. Writing
          // to it after being superseded would put this video's lines into the
          // next one's.
          if (generation === _passGeneration) _tlCache.set(text, vi)
        })
        if (banked.size > 0) void saveNarrationCache(videoId, banked)

        if (generation !== _passGeneration) return
        _passDone = countDone()

        // After every batch, not only at the end. The file is what makes the
        // translation a selectable subtitle track, so writing it once at the
        // finish meant a viewer had nothing to select for the several minutes
        // it took to get there.
        void saveNarrationVtt(videoId, toVTT(cues, _tlCache)).then((ok) => {
          if (ok) _vttVersion++
        })
      }

      // A last write with everything in it, in case the final batch was all
      // cache hits and the loop wrote nothing.
      if (generation === _passGeneration) {
        void saveNarrationVtt(videoId, toVTT(cues, _tlCache)).then((ok) => {
          if (ok) _vttVersion++
        })
      }
    } catch (e) {
      // An exception here used to vanish into the finally and surface as
      // "failed" with nothing after it — which is how a crypto.subtle that does
      // not exist outside a secure context looked from the outside.
      if (generation === _passGeneration) {
        _passThrew = e instanceof Error ? e.message : String(e)
        _passPhase = 'failed'
      }
    } finally {
      if (generation === _passGeneration) {
        _passRunning = false
        // Every phase that means "still on the way there". 'no-translator',
        // 'no-subtitles' and 'not-needed' are answers, and an answer must not
        // be overwritten with a failure.
        const unfinished: NarrationPhase[] = [
          'translating',
          'waiting-config',
          'reading-cache',
          'waiting-subtitles',
          'hashing',
        ]
        if (unfinished.includes(_passPhase)) {
          // 'failed', never 'idle'. Falling back to idle meant a pass that ran
          // to the end and got nothing usable back reported itself as "not
          // started" — the one description guaranteed to send whoever reads it
          // looking in the wrong place. It was reported twice before this.
          _passPhase =
            _passTotal > 0 && _passDone >= _passTotal ? 'done' : 'failed'
        }
      }
    }
  })()
}

/**
 * Thrown for a line that cannot be said in the time it has, even at MAX_SPEED.
 *
 * A distinct type because the answer is different: this cue is skipped and the
 * next one plays as normal, whereas a synthesiser that is down means backing
 * off and trying the same line again later. Told apart by type rather than by
 * reading a message, so a reworded error cannot silently turn one into the
 * other.
 */
export class TooFastError extends Error {
  constructor(readonly neededSpeed: number) {
    super(`needs ${neededSpeed.toFixed(2)}x, above the maximum`)
    this.name = 'TooFastError'
  }
}

/** What the gateway answers with when a line will not fit at any usable tempo. */
const STATUS_TOO_FAST = 422

/**
 * Prepare one line, fitted to the time it has.
 *
 * `slotSeconds` is sent instead of a tempo, and the reason is that the two
 * halves of that sum live in different places: the browser knows how much room
 * a line has (the gap between two subtitle timestamps) while only the
 * synthesiser knows how long the line takes to say. This used to be settled by
 * synthesising once, measuring, and asking again at a corrected tempo — and
 * since tempo is part of the cache key, that second request was always a miss.
 * The lines that needed hurrying were therefore the slowest to arrive, and were
 * then dropped for arriving late. One request now, and the server keeps an
 * unstretched copy so the model runs once per line however the timing changes.
 */
/**
 * The Vietnamese for a cue, or nothing if the translator has not got there.
 *
 * A cue the pass has not reached yet is skipped, not spoken in English: reading
 * the source language aloud under a setting that promised Vietnamese is worse
 * than saying nothing and carrying on.
 */
export function vietnameseFor(text: string): string | undefined {
  if (_sourceLang !== 'en') return text
  return _tlCache.get(text)
}

/**
 * Ask the gateway for one clip, fitted to its slot. Returns the raw bytes.
 *
 * Split out from fetchTTS because the pre-generation sweep wants the work done
 * and the file on disk, but must NOT decode: a decoded clip is an AudioBuffer,
 * and a two-hour video runs to well over a thousand of them. Holding those to
 * save a local disk read would trade gigabytes of memory for milliseconds.
 */
async function requestTTS(
  viText: string,
  slotSeconds: number,
  signal?: AbortSignal,
): Promise<ArrayBuffer> {
  const resp = await fetch('/api/tts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    // videoId tells the gateway where to keep the clip. Synthesis is the
    // expensive half of narration and its bytes never change for the same
    // words at the same tempo, so it belongs on disk beside the video.
    body: JSON.stringify({
      text: viText,
      voice: _voice,
      slotSeconds,
      videoId: _passVideoId,
    }),
    signal,
  })
  if (resp.status === STATUS_TOO_FAST) {
    const body = (await resp.json().catch(() => ({}))) as { neededSpeed?: number }
    throw new TooFastError(body.neededSpeed ?? 0)
  }
  if (!resp.ok) throw new Error(`tts ${resp.status}`)
  return resp.arrayBuffer()
}

/**
 * Put one line on disk at the gateway, without decoding it.
 *
 * This is what the pre-generation sweep runs. It throws `not translated yet`
 * for a line the translator has not reached, which the sweep reads as "come
 * back to this" rather than as a failure.
 */
export async function prepareClip(
  text: string,
  slotSeconds: number,
  signal: AbortSignal,
): Promise<void> {
  const viText = vietnameseFor(text)
  if (!viText) throw new Error('not translated yet')
  await requestTTS(viText, slotSeconds, signal)
}

async function fetchTTS(
  ctx: AudioContext,
  text: string,
  slotSeconds: number,
): Promise<AudioBuffer> {
  const viText = vietnameseFor(text)
  if (!viText) throw new Error('not translated yet')

  // Voice belongs in the key for the same reason it belongs in the server's:
  // without it, changing voice keeps playing whichever one was synthesised
  // first, and this cache would go on doing so even after the disk cache was
  // fixed.
  //
  // The slot is in the key rather than the tempo, because the tempo is no
  // longer known here — it is what the server works out. Two cues with the same
  // words and the same room get the same audio, which is the only case where
  // sharing an entry would be correct anyway.
  const cacheKey = `${viText}@@${slotSeconds.toFixed(2)}@@${_voice}`
  const c = _cache.get(cacheKey)
  if (c) return c
  const inflight = _active.get(cacheKey)
  if (inflight) return inflight
  while (_active.size >= MAX_CONCURRENT) {
    await Promise.race(_active.values()).catch(() => {})
  }
  const p = (async () => {
    // Usually a disk read at the gateway rather than a synthesis, because the
    // pre-generation sweep has already been here. That is the whole point of
    // the sweep: by the time playback asks, the expensive half is done.
    const buf = await ctx.decodeAudioData(await requestTTS(viText, slotSeconds))
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
// Whether a fetch for the cues is actually running. Without it, waiting was
// indistinguishable from waiting for nobody — see whenCuesReady.
let _cuesLoading = false
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

/** Playback position when a clip was last placed, for the watchdog below. */
let _lastSpokeAt = 0

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
  // Already loaded, or already being loaded. Both are the same answer.
  //
  // This used to require `_cues !== null`, which is exactly false while a fetch
  // is in flight — so calling it again for the *same* URL tore down its own
  // request: `_generation++` invalidated the response that was on its way, and
  // the handler dropped it at the generation check. `_cues` stayed null, so the
  // next call did it again.
  //
  // That is self-perpetuating rather than a one-off. Every re-render that
  // reached here restarted the fetch and discarded the previous one, and the
  // translation pass sat on "Loading subtitles…" against subtitles already
  // listed on screen — until something changed the URL and broke the cycle,
  // which is why picking a different track appeared to fix it.
  if (url === _cuesURL) return
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
  _cuesLoading = true
  void fetch(url)
    .then(async (resp) => {
      if (!resp.ok) throw new Error(`VTT ${resp.status}`)
      return parseVTT(await resp.text(), lang)
    })
    .then((cues) => {
      if (generation !== _generation) return
      _cuesLoading = false
      _cues = cues
      announceCues(cues)
      void saveNarrationCues(_passVideoId, cues)
    })
    .catch(() => {
      if (generation !== _generation) return
      _cuesLoading = false
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

    // Nothing to say for it yet.
    //
    // The cursor stays where it is, and the next tick — a tenth of a second —
    // tries again. It used to move on, and moving on is permanent: the cursor
    // only ever goes forwards, so a line the translator had not reached was
    // lost for the rest of the video. On a video already on disk the
    // translations are cached and there is nothing to wait for, which is why
    // this only ever showed itself on a *new* one, and why seeking appeared to
    // "wake narration up" — a seek is the one thing that puts the cursor back.
    //
    // Waiting cannot become stuck, because the check below gives the cue up
    // once the playhead has gone past it. Translation runs two to four times
    // ahead of speech, so in practice the wait is a tick or two.
    if (!vietnameseFor(cue.text) && cue.start >= video.currentTime) {
      reportSkip(index, 'waiting for the translation')
      return
    }

    _cursor++

    // Already spoken past, or already under way — the moment has gone.
    if (cue.start < video.currentTime) {
      reportSkip(index, 'cue already behind the playhead', {
        cueStart: cue.start,
        now: video.currentTime,
      })
      continue
    }

    // Warm the next few in the background. They land in the cache, so when the
    // cursor reaches them there is nothing left to wait for. Deliberately not
    // awaited: this is the concurrency, and it is safe precisely because it
    // cannot commit anything.
    //
    // A safety net rather than the main supply — narration-pregen.ts walks the
    // whole video ahead of playback, so by the time the cursor arrives these
    // are almost always cached already. It stays for the case pre-generation
    // has not covered yet: the first seconds after switching narration on, and
    // a seek landing somewhere the sweep has not reached.
    for (let k = index + 1; k <= index + LOOKAHEAD_CUES && k < cues.length; k++) {
      void fetchTTS(ctx, cues[k].text, slotFor(cues, k)).catch(() => undefined)
    }

    let buffer: AudioBuffer
    try {
      // One request, and the tempo is the server's answer rather than this
      // loop's guess. The two-pass fit that used to live here — synthesise at
      // DEFAULT_SPEED, measure, ask again at a corrected tempo — was a
      // guaranteed cache miss on the second pass, taken synchronously in the
      // middle of committing. It made long lines the slowest to arrive and so
      // the likeliest to be dropped for lateness, which is the gap viewers
      // reported.
      buffer = await fetchTTS(ctx, cue.text, slotFor(cues, index))
    } catch (e) {
      reportSkip(index, 'no clip', e instanceof Error ? e.message : String(e))
      // Including TooFastError: a line that cannot be said in its slot at a
      // followable pace is skipped, and the line after it plays on time. The
      // old behaviour squeezed it to MAX_SPEED and played it anyway, which cost
      // two lines to keep one — it was gibberish at that tempo, and it overran,
      // which pushed the next clip past its own cue.
      continue
    }

    if (generation !== _generation || video.paused) return

    // The clip is ready — but is it still due? Anything that arrived after its
    // own moment is dropped rather than played late, because a late clip talks
    // over the line after it and pushes everything behind it further out. This
    // is what makes a fixed head start unnecessary: narration simply begins at
    // the first line the machine was quick enough for.
    if (!shouldPlay(cue.start, video.currentTime)) {
      reportSkip(index, 'clip arrived after its moment', {
        cueStart: cue.start,
        now: video.currentTime,
      })
      continue
    }

    if (ctx.state === 'suspended') void ctx.resume()

    const due = startTimeFor(cue.start, video.currentTime, ctx.currentTime)
    // Never on top of the clip before it. With commits ordered, _scheduledUntil
    // really is the previous clip's end, so this is a fact rather than a guess.
    const when = scheduleAt(due, _scheduledUntil)

    // Being queued behind an overrun is how narration slid behind the picture:
    // a cue whose audio does not fit even at 3x pushes the next clip, which
    // pushes the one after it, and nothing ever compared the moment a clip
    // actually got against the line it was reading. Dropping one lets the queue
    // catch up — the same trade shouldPlay already makes a few lines above.
    if (tooLateToPlay(when, due)) {
      reportSkip(index, 'queued too far behind its cue', {
        lateBy: (when - due).toFixed(2),
      })
      continue
    }

    _scheduledUntil = scheduleBuffer(ctx, buffer, when)
    _lastSpokeAt = video.currentTime
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
/** Where the pump has got to. Exposed so the effect of an interruption on it
 *  can be asserted; nothing outside this module writes it. */
export function narrationCursor(): number {
  return _cursor
}

export function bindNarration(video: HTMLVideoElement): () => void {
  /**
   * Throw away everything placed, and put the cursor back where the video is.
   *
   * Both halves matter, and pause used to do only the first. Clips are placed
   * up to PREFETCH_SEC — a minute — ahead of the playhead, and the cursor moves
   * with them. Stopping the sources without rewinding left the cursor a minute
   * in the future, so pressing play again skipped every cue in between: the
   * voice went quiet for up to a minute and read as broken.
   */
  const rewindToPlayhead = () => {
    _generation++
    stopEverything()
    _pumping = false
    _cursor = _cues ? firstCueAtOrAfter(_cues, video.currentTime) : 0
    _lastTime = video.currentTime
    _lastSpokeAt = video.currentTime
  }

  /**
   * A jump also moves the preparation queue, but does not empty it.
   *
   * Every cue is wanted eventually — the sweep covers the whole video — so what
   * a seek changes is the order, not the set. Clips already on disk stay there,
   * which is what keeps two turns of the scrub bar from costing a video's worth
   * of synthesis. Only pause and playback position are this listener's other
   * business; deliberately not attached to `pause`, because preparation carries
   * on while the picture is stopped.
   */
  const onSeek = () => {
    rewindToPlayhead()
    seekNarrationPregen(video.currentTime)
  }

  video.addEventListener('pause', rewindToPlayhead)
  video.addEventListener('ended', rewindToPlayhead)
  video.addEventListener('seeking', onSeek)

  return () => {
    video.removeEventListener('pause', rewindToPlayhead)
    video.removeEventListener('ended', rewindToPlayhead)
    video.removeEventListener('seeking', onSeek)
  }
}

/**
 * Diagnostics for the fault where narration goes silent after the downloaded
 * file replaces the upstream stream, and only comes back when the switch is
 * turned off and on again.
 *
 * Two explanations survive reading the code, and they need different fixes:
 *
 *  (a) the AudioContext is suspended and `resume()` is being refused. This fits
 *      the symptom exactly, because toggling the switch is a user gesture and a
 *      gesture is what the browser is holding out for. The same shape as the
 *      bug already recorded above, for a fresh page load.
 *  (b) the cursor is put somewhere the pump then walks past, so every cue in a
 *      stretch is skipped without a single request being made.
 *
 * The difference is visible in one line of output, and guessing between them
 * would mean shipping two fixes and learning nothing. Off unless asked for:
 * `localStorage.setItem('yt-narration-debug', '1')` in the console, reproduce
 * once, read the log.
 */
function narrationDebug(): boolean {
  try {
    return window.localStorage.getItem('yt-narration-debug') === '1'
  } catch {
    return false
  }
}

/** The source the last tick saw, so a swap can be reported as it happens. */
let _lastSrc = ''

function reportSwap(video: HTMLVideoElement, ctx: AudioContext) {
  if (!narrationDebug()) return
  if (video.currentSrc === _lastSrc) return
  const from = _lastSrc
  _lastSrc = video.currentSrc
  if (!from) return
  console.info('[narration] source swapped', {
    // (a) — if this is anything but "running", the clock every scheduled time
    // is measured against has stopped, and nothing placed will ever play.
    audioContext: ctx.state,
    // (b) — cursor against the playhead. A cursor far ahead of where the video
    // is means the cues in between have already been stepped over.
    cursor: _cursor,
    cueCount: _cues?.length ?? 0,
    cueAtPlayhead: _cues ? firstCueAtOrAfter(_cues, video.currentTime) : -1,
    generation: _generation,
    currentTime: video.currentTime.toFixed(2),
    paused: video.paused,
    scheduledUntil: _scheduledUntil.toFixed(2),
    activeClips: _activeSources.size,
    from: from.slice(-60),
    to: video.currentSrc.slice(-60),
  })
}

/** Why a cue produced no sound, when it produced none. */
function reportSkip(index: number, reason: string, detail?: unknown) {
  if (!narrationDebug()) return
  console.info('[narration] cue skipped', { index, reason, detail })
}

/**
 * Called on a timer while narration is on. Keeps the pump fed, follows the
 * viewer's volume, and catches a seek the events missed.
 */
export function tickNarration(video: HTMLVideoElement, ctx: AudioContext) {
  const cues = _cues
  const now = video.currentTime
  reportSwap(video, ctx)

  if (video.paused) {
    stopEverything()
    _lastTime = now
    return
  }

  // A suspended context has a frozen clock, and every time in this file is
  // measured against it.
  //
  // This is what made narration silent until the switch was turned off and on
  // again. Read aloud is remembered across page loads, so on a fresh page the
  // context is built during render — with no user gesture behind it, which is
  // the one condition under which the browser starts it suspended. `currentTime`
  // then stays at zero however long the video plays, so clips were scheduled
  // against a clock that had never started: placed in what the context still
  // considered its opening moment, and dropped as too late to be worth playing.
  // Toggling the switch appeared to fix it because that path creates and resumes
  // the context inside the click, which is exactly what the policy asks for.
  //
  // Resuming here covers the browsers that allow it once the page has been
  // interacted with at all; the player also asks at the next real gesture, for
  // the ones that do not. Either way nothing is scheduled until the clock runs.
  if (ctx.state !== 'running') {
    void ctx.resume()
    _lastTime = now
    return
  }

  if (!_masterGain) {
    _masterGain = ctx.createGain()
    connectOutput(ctx, _masterGain)
  }
  _masterGain.gain.setValueAtTime(_narrationGain, ctx.currentTime)

  // A jump in either direction abandons the timeline: everything scheduled was
  // placed against a playhead that no longer exists.
  if (Math.abs(now - _lastTime) > 0.5) {
    _generation++
    stopEverything()
    _pumping = false
    _cursor = cues ? firstCueAtOrAfter(cues, now) : 0
    // Rebased with the cursor. Without this a jump forwards looks to the
    // watchdog like minutes of silence and a jump backwards makes the figure
    // negative — neither is a stall, both are a seek.
    _lastSpokeAt = now
  }
  _lastTime = now

  if (!cues || cues.length === 0 || _pumping) return

  // The net underneath the three faults this was written after.
  //
  // Each of them ended the same way: something decided not to speak and nothing
  // revisited the decision, so the viewer's own remedy was to seek — the one
  // thing that puts the cursor back. All three are fixed at their own root;
  // this is here so a fourth of the same shape costs a few silent seconds
  // rather than the rest of the video.
  //
  // Narrow on purpose. It asks one question about one thing, rather than
  // re-asserting everything every second — see narration-watchdog.ts, and
  // CLAUDE.md §4 on why a general sweep is worse than the faults it hides.
  const atPlayhead = firstCueAtOrAfter(cues, now)
  if (
    hasStalled({
      wanted: true,
      playing: !video.paused,
      scheduled: _activeSources.size,
      cursor: _cursor,
      cursorAtPlayhead: atPlayhead,
      silentFor: now - _lastSpokeAt,
    })
  ) {
    _generation++
    stopEverything()
    _cursor = atPlayhead
    _lastSpokeAt = now
  }

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

/**
 * Stop speaking and forget the cue list.
 *
 * Deliberately does NOT touch the translation pass. This runs from the cleanup
 * of the narration tick effect, which tears down whenever the front <video>
 * changes identity — a layer swap, not a new video. Cancelling the pass here
 * killed it seconds after it started and left the status reading "not started"
 * with nothing to restart it, because the effect that starts a pass has
 * different dependencies and had no reason to run again.
 *
 * Ending a pass is cancelTranslationPass, and it belongs to changing video.
 */
/**
 * Stop speaking. Keep the cues.
 *
 * This is what the narration tick loop wants when it tears down, and the tick
 * loop tears down whenever the output mode stops including a voice — switching
 * from "Giọng đọc" to "Phụ đề" is enough. It used to call resetNarration, which
 * also discarded the cue list; and the effect that loads cues depends on
 * narration being on rather than on it speaking, so it had no reason to run
 * again. Switching back left _cues null with nothing to refill it, and the tick
 * returned at its first line without ever asking for a single clip.
 */
export function stopNarrationPlayback() {
  _generation++
  _cursor = 0
  _pumping = false
  _lastSpokeAt = 0
  // Zeroed so the next tick reads as a jump and re-derives the cursor from
  // wherever the video has got to in the meantime.
  _lastTime = 0
  _masterGain = null
  stopEverything()
}

/** Stop speaking and forget the cue list. For leaving the video behind. */
export function resetNarration() {
  _cues = null
  _cuesURL = ''
  _cuesLoading = false
  stopNarrationPlayback()
}


// ---- pre-generation ---------------------------------------------------------

/**
 * The sweep that prepares a whole video's narration ahead of playback.
 *
 * Built here rather than inside narration-pregen.ts so that module stays free
 * of fetch, of module state, and of this file's caches — which is what makes
 * its retry, backoff and cancellation rules testable without a browser.
 */
const _pregen = createPregen({
  prepare: prepareClip,
  isTranslated: (text) => vietnameseFor(text) !== undefined,
  setTimer: (fn, ms) => window.setTimeout(fn, ms),
  clearTimer: (h) => window.clearTimeout(h as number),
  sleep: (ms) => new Promise((r) => window.setTimeout(r, ms)),
  now: () => Date.now(),
})

/**
 * What the sweep is doing, for the status line.
 *
 * The estimate is applied here rather than inside the sweep, reusing the
 * translation pass's arithmetic unchanged — the question is identical, and two
 * implementations of "how long is left" would drift apart and disagree on the
 * same panel.
 *
 * Quoted only while actually synthesising. Waiting on the translator or sitting
 * out a backoff are both states where the number would keep counting down
 * against work that is not happening, which is worse than no number at all.
 */
export function pregenProgress(): ReturnType<typeof _pregen.progress> & {
  etaSeconds: number | null
} {
  const p = _pregen.progress()
  return {
    ...p,
    etaSeconds:
      p.phase === 'sweeping'
        ? estimateEtaSeconds({
            done: p.done,
            total: p.total,
            baseline: p.baseline,
            elapsedMs: p.startedAt ? Date.now() - p.startedAt : 0,
          })
        : null,
  }
}

function pregenLines(cues: CueText[]) {
  return cues.map((_, i) => ({
    text: cues[i].text,
    slotSeconds: slotFor(cues, i),
  }))
}

/**
 * Begin preparing this video's narration, from wherever the viewer is.
 *
 * Safe to call repeatedly: a sweep already running for the same video is left
 * alone rather than restarted.
 */
export function startNarrationPregen(videoId: string, atTime: number) {
  void (async () => {
    // The cue list is fetched and parsed asynchronously, so at the moment the
    // player decides to narrate there is usually nothing here yet. Waiting on
    // the load has no deadline to get wrong — the same reasoning that replaced
    // the translation pass's ten-second poll.
    const cues = await whenCuesReady()
    // A video with no subtitles has nothing to say, and a video the viewer has
    // already moved on from is not ours to prepare.
    if (cues.length === 0 || videoId !== _passVideoId) return
    _pregen.start({
      videoId,
      lines: pregenLines(cues),
      fromIndex: nearestCueIndex(cues, atTime),
    })
  })()
}

/** Move the queue to where the viewer has just jumped to. */
export function seekNarrationPregen(atTime: number) {
  const cues = _cues
  if (!cues || cues.length === 0) return
  _pregen.seek(nearestCueIndex(cues, atTime))
}

/**
 * Stop preparing, for good.
 *
 * Belongs to the player being torn down — a new video, or the miniplayer being
 * closed — and NOT to leaving the watch page, where the miniplayer carries on
 * talking and still needs clips.
 */
export function cancelNarrationPregen() {
  _pregen.cancel()
}

/**
 * Throw away every clip and prepare them again, in the newly chosen voice.
 *
 * The in-memory cache is keyed by voice, so nothing here can be reused; and the
 * copies on disk are cleared by the caller, which owns the request. The
 * translation cache is deliberately untouched: Vietnamese text does not depend
 * on who reads it.
 */
export function restartNarrationPregenForVoice() {
  _cache.clear()
  _active.clear()
  _pregen.restart()
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
