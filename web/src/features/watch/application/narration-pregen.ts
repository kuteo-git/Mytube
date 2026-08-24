/**
 * Synthesise a whole video's narration ahead of playback.
 *
 * Narration used to be prepared inside the playback loop, three cues ahead of
 * the playhead. Anything not ready by the time its cue arrived was dropped —
 * silently, and disproportionately the long lines, because those needed a
 * second request at a corrected tempo which was always a cache miss. The result
 * was speech that stopped for a stretch and then carried on.
 *
 * So preparation is lifted out of playback entirely and becomes a sweep, in the
 * same shape as the translation pass that already worked this way. Every clip
 * is on disk before its moment, so nothing is late, so nothing is dropped for
 * being late.
 *
 * **It never decodes.** A sweep asks the gateway to synthesise and throws the
 * bytes away; the point is the copy the gateway keeps on disk. Decoding would
 * mean holding an AudioBuffer per cue, and a two-hour video is upwards of a
 * thousand cues — gigabytes of RAM to save a disk read of a few milliseconds.
 * The small hot cache near the playhead stays in narration.ts, where it is
 * bounded by how far ahead playback looks.
 */
import { workOrder } from './narration-batch'
import { BATCH_ATTEMPTS, GIVE_UP_AFTER, retryDelayMs } from './narration-retry'

/** Requests in flight at once. The synthesiser is shared with the whole machine. */
export const PREGEN_CONCURRENCY = 2

/**
 * How long to wait before looking again for lines the translator has not
 * reached yet.
 *
 * A cue cannot be synthesised before it has Vietnamese to say. Translation runs
 * two to four times ahead of speech, so this is a short wait for a queue that is
 * already moving, not a poll against something that may never arrive.
 */
export const TRANSLATION_POLL_MS = 2_000

/**
 * Waits before sweeping again after the sweep gave up entirely.
 *
 * Giving up means the synthesiser is down, and CLAUDE.md §8 records what this
 * project already paid for pushing against a wall that was not going to move.
 * But unlike the translation pass, stopping for good is not an option here: a
 * translation that dies leaves a status line and a viewer who can restart it,
 * while narration that dies just goes quiet — indistinguishable from the very
 * fault this file exists to fix. So it stops, waits, and tries again.
 */
export const RETRY_BACKOFF_MS = [15_000, 30_000, 60_000]

export type PregenPhase =
  | 'idle'
  | 'sweeping'
  /** Everything that can be synthesised has been. */
  | 'done'
  /** Waiting for the translator to reach the lines that are left. */
  | 'awaiting-translation'
  /** The synthesiser failed repeatedly; a retry is scheduled. */
  | 'backing-off'

export interface PregenDeps {
  /**
   * Put one line on disk at the gateway. Resolves when it is there.
   *
   * Rejecting with a TooFastError-shaped error means the line cannot be said in
   * its slot at a followable pace: that is an answer, not a failure, and the
   * cue is marked done so no retry is spent on it.
   */
  prepare(text: string, slotSeconds: number, signal: AbortSignal): Promise<void>
  /** Whether this line has Vietnamese to say yet. */
  isTranslated(text: string): boolean
  /**
   * Schedule the next sweep. Injected so tests do not wait in real time.
   *
   * Deliberately separate from `sleep` below. They were one call at first, and
   * that was a mistake in two directions: the pause between two attempts at the
   * same line and the wait before an entire sweep is retried are different
   * things with different lifetimes, and sharing a handle meant a cancelled
   * sweep left its retry pause running with nothing able to clear it.
   */
  setTimer(fn: () => void, ms: number): unknown
  clearTimer(handle: unknown): void
  /** Wait between two attempts at the same line. */
  sleep(ms: number): Promise<void>
  /** Injected for the same reason the timers are: tests must not wait. */
  now(): number
  /**
   * Where the sweep says what it is doing. Optional, and a dependency rather
   * than an import for the same reason the timers are — this module stays free
   * of the browser.
   *
   * It exists because the status line reported `idle` on a video that was
   * audibly narrating, and nothing anywhere could say which of the several
   * roads to `idle` had been taken: never started, cancelled by the video
   * changing, or a sweep that returned before it set a phase. A label is not
   * evidence; this is.
   */
  log?(event: string, fields: Record<string, unknown>): void
}

export interface PregenTarget {
  videoId: string
  /** Text and slot per cue, in cue order. */
  lines: Array<{ text: string; slotSeconds: number }>
  /** Cue index to work outward from — where the viewer is. */
  fromIndex: number
}

export interface PregenProgress {
  phase: PregenPhase
  done: number
  total: number
  /** How many lines were refused as unsayable in their slot. */
  tooFast: number
  videoId: string
  /**
   * When the current sweep began, and how much was already done by then.
   *
   * Reported as two numbers rather than as an estimate, so the arithmetic stays
   * in estimateEtaSeconds where the translation pass's version of this question
   * is already answered and already tested.
   *
   * The baseline matters for the same reason it does there: clips that came off
   * disk in microseconds say nothing about how fast the synthesiser is going,
   * and counting them would report almost no work left while minutes of it
   * remained. A sweep resumed after a backoff starts from what it had already
   * settled, which is exactly the figure to discount.
   */
  startedAt: number
  baseline: number
}

/** Distinguishes "this line cannot fit" from "the synthesiser is unwell". */
function isTooFast(e: unknown): boolean {
  return e instanceof Error && e.name === 'TooFastError'
}

function isAbort(e: unknown): boolean {
  return e instanceof Error && (e.name === 'AbortError' || e.name === 'CancelledError')
}

export interface Pregen {
  start(target: PregenTarget): void
  /**
   * Move the queue to a new position.
   *
   * The work already done is kept: every cue is wanted eventually, so a seek
   * changes the order rather than the set. What it does stop is whatever was
   * being prepared for somewhere the viewer has just left.
   */
  seek(fromIndex: number): void
  /** Stop everything and schedule nothing. For leaving the video for good. */
  cancel(): void
  /**
   * Forget every clip and sweep again — the voice changed, so nothing already
   * synthesised can be used.
   */
  restart(): void
  progress(): PregenProgress
}

export function createPregen(deps: PregenDeps): Pregen {
  let generation = 0
  let target: PregenTarget | null = null
  let phase: PregenPhase = 'idle'
  let abort: AbortController | null = null
  let timer: unknown = null
  let backoffStep = 0
  /** Cue indices that need nothing further — synthesised, or refused as unsayable. */
  let settled = new Set<number>()
  let tooFastCount = 0
  let running = false
  let startedAt = 0
  let baseline = 0

  const clearTimer = () => {
    if (timer !== null) {
      deps.clearTimer(timer)
      timer = null
    }
  }

  /**
   * Stop the current sweep without deciding what happens next.
   *
   * The generation bump is what stops work already in flight: every await in
   * the sweep is followed by a check, so a superseded pass returns rather than
   * writing anything.
   */
  const say = (event: string, fields: Record<string, unknown> = {}) =>
    deps.log?.(event, { phase, videoId: target?.videoId ?? '', ...fields })

  const halt = () => {
    generation++
    abort?.abort()
    abort = null
    clearTimer()
    running = false
  }

  const later = (fn: () => void, ms: number) => {
    clearTimer()
    const gen = generation
    timer = deps.setTimer(() => {
      timer = null
      // A timer that fires after the video has moved on must do nothing. This
      // is the check that keeps a watchdog from outliving what it was watching.
      if (gen !== generation) return
      fn()
    }, ms)
  }

  /** Prepare one line, with retries. Returns whether it is now settled. */
  const prepareOne = async (
    index: number,
    gen: number,
  ): Promise<'settled' | 'failed' | 'stale'> => {
    const line = target!.lines[index]
    for (let attempt = 0; attempt < BATCH_ATTEMPTS; attempt++) {
      if (gen !== generation) return 'stale'
      if (attempt > 0) {
        await deps.sleep(retryDelayMs(attempt))
        if (gen !== generation) return 'stale'
      }
      try {
        await deps.prepare(line.text, line.slotSeconds, abort!.signal)
        return 'settled'
      } catch (e) {
        if (gen !== generation || isAbort(e)) return 'stale'
        if (isTooFast(e)) {
          // Not a failure: the line will never fit, so asking again would get
          // the same answer. Counted separately because a video full of these
          // means the translations are too long, which is a prompt problem
          // rather than a synthesiser one.
          tooFastCount++
          return 'settled'
        }
        // Every other failure is worth another go. Deliberately not routed
        // through worthRetrying: that function reads the *translator's* error
        // text, and its "answered but returned nothing usable" rule has no
        // meaning for a synthesiser that either produces audio or does not.
      }
    }
    return 'failed'
  }

  const sweep = async () => {
    if (!target || running) {
      // Both of these leave the phase exactly as it was, which is the whole
      // reason this is worth a line: a sweep that never began looks identical
      // to one that has not been asked for.
      say('pregen sweep refused', { hasTarget: target !== null, running })
      return
    }
    running = true
    const gen = generation
    abort = new AbortController()
    phase = 'sweeping'
    say('pregen sweeping', { settled: settled.size })
    // Timed from here rather than from start(), because everything before this
    // was waiting — for the translator, or out a backoff — and counting it
    // would make every estimate gloomier than the work deserves.
    startedAt = deps.now()
    baseline = settled.size

    let consecutiveFailures = 0
    let deferred = 0

    try {
      // From the playhead to the end, then wrapping to cover the beginning —
      // the same ordering the translation pass uses, so the line the viewer
      // needs next is first in the queue while the whole video still gets
      // covered.
      const queue = workOrder(target.lines.length, target.fromIndex).filter(
        (i) => !settled.has(i),
      )

      let next = 0
      const worker = async () => {
        while (next < queue.length) {
          if (gen !== generation) return
          const index = queue[next++]
          if (settled.has(index)) continue

          if (!deps.isTranslated(target!.lines[index].text)) {
            // Left for a later sweep rather than skipped for good. Unlike
            // playback, a sweep has no moment to miss — it can simply come back.
            deferred++
            continue
          }

          const outcome = await prepareOne(index, gen)
          if (gen !== generation) return
          if (outcome === 'stale') return
          if (outcome === 'settled') {
            settled.add(index)
            consecutiveFailures = 0
            // One success says the synthesiser is well again, so the next
            // outage starts its backoff from the bottom rather than from
            // wherever the last one left off.
            backoffStep = 0
            continue
          }

          consecutiveFailures++
          if (consecutiveFailures >= GIVE_UP_AFTER) return
        }
      }

      await Promise.all(
        Array.from({ length: PREGEN_CONCURRENCY }, () => worker()),
      )

      if (gen !== generation) return

      if (consecutiveFailures >= GIVE_UP_AFTER) {
        phase = 'backing-off'
      say('pregen phase')
        const wait = RETRY_BACKOFF_MS[Math.min(backoffStep, RETRY_BACKOFF_MS.length - 1)]
        backoffStep++
        later(() => void sweep(), wait)
        return
      }

      if (deferred > 0) {
        phase = 'awaiting-translation'
      say('pregen phase')
        later(() => void sweep(), TRANSLATION_POLL_MS)
        return
      }

      phase = 'done'
      say('pregen phase')
    } catch {
      // An exception must not end the sweep for good. narration.ts records what
      // that costs: work that throws on the way out leaves a running flag set,
      // and a flag stuck true is the one state nothing can restart from.
      if (gen !== generation) return
      phase = 'backing-off'
      say('pregen threw')
      const wait = RETRY_BACKOFF_MS[Math.min(backoffStep, RETRY_BACKOFF_MS.length - 1)]
      backoffStep++
      later(() => void sweep(), wait)
    } finally {
      if (gen === generation) running = false
    }
  }

  return {
    start(next: PregenTarget) {
      // Already sweeping this video: leave it be. Restarting would throw away
      // an in-flight clip and reorder a queue that is already correct.
      if (target?.videoId === next.videoId && phase !== 'idle') {
        say('pregen start ignored', { wanted: next.videoId })
        return
      }
      say('pregen start', { wanted: next.videoId, lines: next.lines.length })
      halt()
      target = next
      settled = new Set()
      tooFastCount = 0
      backoffStep = 0
      void sweep()
    },

    seek(fromIndex: number) {
      if (!target) return
      halt()
      target = { ...target, fromIndex }
      void sweep()
    },

    cancel() {
      say('pregen cancel')
      halt()
      target = null
      phase = 'idle'
    },

    restart() {
      if (!target) return
      halt()
      // Everything on disk was read in the old voice, so nothing counts as done.
      settled = new Set()
      tooFastCount = 0
      backoffStep = 0
      void sweep()
    },

    progress(): PregenProgress {
      return {
        phase,
        done: settled.size,
        total: target?.lines.length ?? 0,
        tooFast: tooFastCount,
        videoId: target?.videoId ?? '',
        startedAt,
        baseline,
      }
    },
  }
}
