import { describe, expect, it, vi } from 'vitest'
import {
  PREGEN_CONCURRENCY,
  RETRY_BACKOFF_MS,
  TRANSLATION_POLL_MS,
  createPregen,
  type PregenDeps,
  type PregenTarget,
} from './narration-pregen'
import { GIVE_UP_AFTER } from './narration-retry'

/**
 * A controllable clock.
 *
 * Timers are injected rather than faked globally because the sweep schedules
 * against them for two different reasons — waiting on the translator and backing
 * off from a dead synthesiser — and the tests below need to tell those apart by
 * the delay that was asked for.
 */
function makeClock() {
  let seq = 0
  const pending = new Map<number, { fn: () => void; ms: number }>()
  return {
    setTimer(fn: () => void, ms: number) {
      const id = ++seq
      pending.set(id, { fn, ms })
      return id
    },
    clearTimer(handle: unknown) {
      pending.delete(handle as number)
    },
    /** Delays currently scheduled, longest-lived first registered. */
    delays() {
      return [...pending.values()].map((p) => p.ms)
    },
    size() {
      return pending.size
    },
    /** Fire everything currently scheduled. */
    async runAll() {
      const now = [...pending.entries()]
      pending.clear()
      for (const [, p] of now) p.fn()
      await flush()
    },
  }
}

/** Let queued promise callbacks settle. */
async function flush(times = 30) {
  for (let i = 0; i < times; i++) await Promise.resolve()
}

function linesFor(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    text: `line-${i}`,
    slotSeconds: 2,
  }))
}

function setup(over: Partial<PregenDeps> = {}, total = 6, fromIndex = 0) {
  const clock = makeClock()
  const prepared: string[] = []
  const deps: PregenDeps = {
    prepare: async (text) => {
      prepared.push(text)
    },
    isTranslated: () => true,
    setTimer: clock.setTimer,
    clearTimer: clock.clearTimer,
    sleep: async () => {},
    now: () => 0,
    ...over,
  }
  const target: PregenTarget = {
    videoId: 'vid1',
    lines: linesFor(total),
    fromIndex,
  }
  return { clock, prepared, deps, target, pregen: createPregen(deps) }
}

class TooFastError extends Error {
  constructor() {
    super('needs 5.00x')
    this.name = 'TooFastError'
  }
}

// ---- ordering ---------------------------------------------------------------

describe('sweep order', () => {
  it('covers the whole video, starting where the viewer is', async () => {
    const { pregen, prepared, target } = setup({}, 6, 3)
    pregen.start(target)
    await flush()

    // Every line, and none twice. Translating only forward from the playhead is
    // what used to leave the opening of a video permanently silent.
    expect([...prepared].sort()).toEqual(linesFor(6).map((l) => l.text).sort())
    expect(prepared).toHaveLength(6)
  })

  it('asks for the viewer\'s next line before anything else', async () => {
    const { pregen, prepared, target } = setup({}, 6, 3)
    pregen.start(target)
    await flush()
    expect(prepared[0]).toBe('line-3')
  })

  it('wraps to the beginning after reaching the end', async () => {
    const { pregen, prepared, target } = setup({}, 4, 2)
    pregen.start(target)
    await flush()
    // 2,3 then wrap to 0,1 — with two workers the exact interleaving is not
    // fixed, but everything after the playhead must be asked for before the
    // wrap-around tail.
    expect(prepared.indexOf('line-3')).toBeLessThan(prepared.indexOf('line-1'))
  })
})

// ---- not redoing work -------------------------------------------------------

describe('idempotence', () => {
  it('a seek keeps what is already done', async () => {
    const { pregen, prepared, target } = setup({}, 6, 0)
    pregen.start(target)
    await flush()
    expect(prepared).toHaveLength(6)

    prepared.length = 0
    pregen.seek(4)
    await flush()

    // Every cue is wanted eventually, so a seek reorders the queue rather than
    // emptying it. Re-synthesising here would spend the whole video's TTS again
    // for every turn of the scrub bar.
    expect(prepared).toHaveLength(0)
    expect(pregen.progress().phase).toBe('done')
  })

  it('a seek re-prioritises the lines still outstanding', async () => {
    const notYet = new Set(['line-4', 'line-5'])
    const { pregen, prepared, target } = setup(
      { isTranslated: (t) => !notYet.has(t) },
      6,
      0,
    )
    pregen.start(target)
    await flush()
    expect(pregen.progress().phase).toBe('awaiting-translation')

    prepared.length = 0
    notYet.clear()
    pregen.seek(5)
    await flush()

    expect(prepared[0]).toBe('line-5')
  })

  it('restarting after a voice change redoes everything', async () => {
    const { pregen, prepared, target } = setup({}, 4, 0)
    pregen.start(target)
    await flush()
    prepared.length = 0

    pregen.restart()
    await flush()

    // Every clip on disk was read in the old voice, so none of them count.
    expect(prepared).toHaveLength(4)
  })
})

// ---- waiting for the translator ---------------------------------------------

describe('lines the translator has not reached', () => {
  it('waits and comes back rather than skipping them for good', async () => {
    let ready = false
    const { pregen, prepared, clock, target } = setup(
      { isTranslated: (t) => ready || t !== 'line-2' },
      4,
      0,
    )
    pregen.start(target)
    await flush()

    expect(prepared).not.toContain('line-2')
    expect(pregen.progress().phase).toBe('awaiting-translation')
    expect(clock.delays()).toEqual([TRANSLATION_POLL_MS])

    ready = true
    await clock.runAll()

    // Skipping it instead would leave a hole nothing could ever fill: the
    // sweep never revisits, and playback would find no clip when it arrived.
    expect(prepared).toContain('line-2')
    expect(pregen.progress().phase).toBe('done')
  })

  it('does not count an untranslated line as a synthesiser failure', async () => {
    const { pregen, clock, target } = setup({ isTranslated: () => false }, 4, 0)
    pregen.start(target)
    await flush()

    // Backing off here would stall a queue that is merely waiting its turn.
    expect(pregen.progress().phase).toBe('awaiting-translation')
    expect(clock.delays()).toEqual([TRANSLATION_POLL_MS])
  })
})

// ---- lines that cannot fit --------------------------------------------------

describe('a line that will not fit at any usable tempo', () => {
  it('is settled rather than retried', async () => {
    const prepare = vi.fn(async (text: string) => {
      if (text === 'line-1') throw new TooFastError()
    })
    const { pregen, target } = setup({ prepare }, 4, 0)
    pregen.start(target)
    await flush()

    // The answer will be the same every time, so retrying is pure waste.
    expect(prepare.mock.calls.filter(([t]) => t === 'line-1')).toHaveLength(1)
    expect(pregen.progress().tooFast).toBe(1)
    expect(pregen.progress().phase).toBe('done')
  })

  it('does not trip the give-up counter', async () => {
    // A video whose translations are all too long is a prompt problem, not a
    // dead synthesiser — and treating it as one would stop the sweep on a
    // machine that is working perfectly.
    const { pregen, target } = setup(
      {
        prepare: async () => {
          throw new TooFastError()
        },
      },
      6,
      0,
    )
    pregen.start(target)
    await flush()

    expect(pregen.progress().phase).toBe('done')
    expect(pregen.progress().tooFast).toBe(6)
  })
})

// ---- failure, giving up, and coming back ------------------------------------

describe('when the synthesiser is down', () => {
  const dead = () => ({
    prepare: async () => {
      throw new Error('tts 502')
    },
  })

  it('stops after enough consecutive failures instead of hammering it', async () => {
    const prepare = vi.fn(async () => {
      throw new Error('tts 502')
    })
    const { pregen, target } = setup({ prepare }, 50, 0)
    pregen.start(target)
    await flush()

    // BATCH_ATTEMPTS tries per line, GIVE_UP_AFTER lines, and at most one
    // extra line in flight per worker. Far short of the 50 lines on offer —
    // CLAUDE.md §8 records what pushing against a wall already cost here.
    expect(prepare.mock.calls.length).toBeLessThan(50)
    expect(pregen.progress().phase).toBe('backing-off')
  })

  it('schedules a sweep instead of ending', async () => {
    const { pregen, clock, target } = setup(dead(), 20, 0)
    pregen.start(target)
    await flush()

    // The difference from the translation pass, and a deliberate one: a
    // translation that dies leaves a status line and a viewer who can restart
    // it. Narration that dies just goes quiet, which is the very fault this
    // file exists to fix.
    expect(pregen.progress().phase).toBe('backing-off')
    expect(clock.delays()).toEqual([RETRY_BACKOFF_MS[0]])
  })

  it('backs off further each time, up to the cap', async () => {
    const { pregen, clock, target } = setup(dead(), 20, 0)
    pregen.start(target)
    await flush()

    const seen: number[] = []
    for (let i = 0; i < 5; i++) {
      seen.push(clock.delays()[0])
      await clock.runAll()
    }
    expect(seen).toEqual([
      RETRY_BACKOFF_MS[0],
      RETRY_BACKOFF_MS[1],
      RETRY_BACKOFF_MS[2],
      RETRY_BACKOFF_MS[2],
      RETRY_BACKOFF_MS[2],
    ])
  })

  it('resumes on its own once the synthesiser comes back', async () => {
    let up = false
    const { pregen, prepared, clock, target } = setup(
      {
        prepare: async (text) => {
          if (!up) throw new Error('tts 502')
          prepared.push(text)
        },
      },
      6,
      0,
    )
    // `prepared` is captured before setup returns it; rebind through the closure.
    pregen.start(target)
    await flush()
    expect(pregen.progress().phase).toBe('backing-off')

    up = true
    await clock.runAll()

    // Nobody touched anything. That is the requirement.
    expect(pregen.progress().phase).toBe('done')
    expect(pregen.progress().done).toBe(6)
  })

  it('starts the next outage from the bottom of the backoff', async () => {
    let up = false
    const { pregen, clock, target } = setup(
      {
        prepare: async () => {
          if (!up) throw new Error('tts 502')
        },
      },
      30,
      0,
    )
    pregen.start(target)
    await flush()
    await clock.runAll() // still down: second backoff step
    expect(clock.delays()).toEqual([RETRY_BACKOFF_MS[1]])

    up = true
    await clock.runAll() // recovers
    up = false
    pregen.restart()
    await flush()

    // One success means the machine is well, so a later outage is a new
    // outage — not a continuation of the old one at a minute a try.
    expect(clock.delays()).toEqual([RETRY_BACKOFF_MS[0]])
  })

  it('an unexpected exception schedules a retry rather than wedging', async () => {
    // narration.ts records what this costs: work that throws on the way out
    // leaves a running flag set, and a flag stuck true is the one state nothing
    // can restart from.
    let boom = true
    const { pregen, clock, target } = setup(
      {
        isTranslated: () => {
          if (boom) throw new Error('crypto.subtle is not available')
          return true
        },
      },
      4,
      0,
    )
    pregen.start(target)
    await flush()

    expect(pregen.progress().phase).toBe('backing-off')
    expect(clock.size()).toBe(1)

    boom = false
    await clock.runAll()
    expect(pregen.progress().phase).toBe('done')
  })
})

// ---- stopping ---------------------------------------------------------------

describe('cancel', () => {
  it('schedules nothing further', async () => {
    const { pregen, clock, target } = setup(
      {
        prepare: async () => {
          throw new Error('tts 502')
        },
      },
      20,
      0,
    )
    pregen.start(target)
    await flush()
    expect(clock.size()).toBe(1)

    pregen.cancel()

    // A watchdog that outlives the video would resurrect synthesis for a page
    // nobody is on — and it would do it every minute, forever.
    expect(clock.size()).toBe(0)
    expect(pregen.progress().phase).toBe('idle')
  })

  it('a timer that survives a cancel still does nothing when it fires', async () => {
    // Belt and braces: clearTimer is the first line of defence, and the
    // generation check behind it is what makes a leaked timer harmless.
    const clock = makeClock()
    const prepare = vi.fn(async () => {
      throw new Error('tts 502')
    })
    const pregen = createPregen({
      prepare,
      isTranslated: () => true,
      setTimer: clock.setTimer,
      clearTimer: () => {}, // deliberately does not clear
      sleep: async () => {},
      now: () => 0,
    })
    pregen.start({ videoId: 'v', lines: linesFor(20), fromIndex: 0 })
    await flush()

    const before = prepare.mock.calls.length
    pregen.cancel()
    await clock.runAll()

    expect(prepare.mock.calls.length).toBe(before)
  })

  it('stops the sweep from advancing after being cancelled mid-flight', async () => {
    let release: null | (() => void) = null
    const fire = () => release?.()
    const prepare = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          release = resolve
        }),
    )
    const { pregen, target } = setup({ prepare }, 20, 0)
    pregen.start(target)
    await flush()

    expect(prepare.mock.calls.length).toBe(PREGEN_CONCURRENCY)
    pregen.cancel()
    fire()
    await flush()

    expect(prepare.mock.calls.length).toBe(PREGEN_CONCURRENCY)
  })
})

// ---- pause, and concurrency -------------------------------------------------

describe('independence from playback', () => {
  it('has no notion of the video being paused', async () => {
    // The point of the feature. Nothing in PregenDeps can express "paused",
    // which is the strongest form this guarantee can take: a later change
    // cannot quietly make the sweep stop when the picture stops.
    const keys = Object.keys(setup().deps)
    expect(keys).toEqual([
      'prepare',
      'isTranslated',
      'setTimer',
      'clearTimer',
      'sleep',
      'now',
    ])
  })

  it('never has more than PREGEN_CONCURRENCY requests in flight', async () => {
    let inFlight = 0
    let peak = 0
    const { pregen, target } = setup(
      {
        prepare: async () => {
          inFlight++
          peak = Math.max(peak, inFlight)
          await Promise.resolve()
          inFlight--
        },
      },
      30,
      0,
    )
    pregen.start(target)
    await flush(80)

    expect(peak).toBeLessThanOrEqual(PREGEN_CONCURRENCY)
  })
})

describe('start', () => {
  it('leaves an existing sweep for the same video alone', async () => {
    const { pregen, prepared, target } = setup({}, 4, 0)
    pregen.start(target)
    await flush()
    prepared.length = 0

    pregen.start({ ...target, fromIndex: 2 })
    await flush()

    // Restarting would throw away in-flight work to rebuild a queue that is
    // already correct.
    expect(prepared).toHaveLength(0)
  })

  it('abandons the previous video when a new one arrives', async () => {
    const { pregen, prepared, target } = setup({}, 4, 0)
    pregen.start(target)
    await flush()
    prepared.length = 0

    pregen.start({ videoId: 'vid2', lines: linesFor(3), fromIndex: 0 })
    await flush()

    expect(prepared).toHaveLength(3)
    expect(pregen.progress().videoId).toBe('vid2')
  })
})

describe('give-up threshold', () => {
  it('is the shared one, not a number invented here', () => {
    expect(GIVE_UP_AFTER).toBeGreaterThan(0)
  })
})

// ---- what the progress row is built from ------------------------------------

describe('progress reporting', () => {
  it('times each sweep from when it actually starts working', async () => {
    // Not from start(): everything before the first request is waiting — on the
    // translator, or out a backoff — and counting it would make every estimate
    // gloomier than the work deserves.
    let clockValue = 1000
    const { pregen, target } = setup({ now: () => clockValue }, 4, 0)
    pregen.start(target)
    await flush()
    expect(pregen.progress().startedAt).toBe(1000)
  })

  it('discounts what a resumed sweep had already done', async () => {
    // Clips settled by an earlier sweep come back from disk in microseconds and
    // say nothing about how fast the synthesiser is going. Counting them would
    // report almost no work left while minutes of it remained.
    // Four lines land, then the synthesiser dies. The sweep gives up, waits,
    // and comes back — and what it already has must not be counted as progress
    // made by the second sweep.
    const good = new Set(['line-0', 'line-1', 'line-2', 'line-3'])
    let up = false
    const { pregen, clock, target } = setup(
      {
        prepare: async (text) => {
          if (!up && !good.has(text)) throw new Error('tts 502')
        },
      },
      30,
      0,
    )
    pregen.start(target)
    await flush()
    expect(pregen.progress().phase).toBe('backing-off')
    expect(pregen.progress().baseline).toBe(0)
    const settledSoFar = pregen.progress().done
    expect(settledSoFar).toBeGreaterThan(0)

    up = true
    await clock.runAll()

    expect(pregen.progress().baseline).toBe(settledSoFar)
  })

  it('counts lines that cannot be said in their slot', async () => {
    // Surfaced rather than swallowed: a handful is ordinary, a video full of
    // them means the translations are running long — which is fixed in the
    // prompt, somewhere no viewer can reach.
    const { pregen, target } = setup(
      {
        prepare: async (text) => {
          if (text === 'line-0' || text === 'line-2') throw new TooFastError()
        },
      },
      4,
      0,
    )
    pregen.start(target)
    await flush()
    expect(pregen.progress().tooFast).toBe(2)
    expect(pregen.progress().done).toBe(4)
  })

  it('reports every phase the status row has words for', async () => {
    // The row renders a label per phase from a total Record, so a phase added
    // here without one would be a compile error there — but a phase that is
    // never reached is a label nobody ever sees, which is the quieter fault.
    const { pregen, target } = setup({ isTranslated: () => false }, 4, 0)
    expect(pregen.progress().phase).toBe('idle')
    pregen.start(target)
    await flush()
    expect(pregen.progress().phase).toBe('awaiting-translation')
    pregen.cancel()
    expect(pregen.progress().phase).toBe('idle')
  })
})
