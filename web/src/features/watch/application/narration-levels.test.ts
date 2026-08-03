import { describe, expect, it } from 'vitest'
import { levelsFor } from './narration-levels'

const base = {
  master: 1,
  muted: false,
  narrating: true,
  narrationLevel: 0.5,
  duckLevel: 0.2,
}

describe('levelsFor', () => {
  it('reproduces the behaviour the old constants gave', () => {
    // NARRATION_GAIN 2.5 applied to a video already ducked to 0.2 came to 0.5
    // of master, with the video at 0.2. The defaults are those two numbers, so
    // nobody's balance changes when this ships.
    expect(levelsFor(base)).toEqual({ narration: 0.5, video: 0.2 })
  })

  it('moves the voice without moving the video', () => {
    // The whole point. Under the old chaining this was impossible.
    const quiet = levelsFor({ ...base, narrationLevel: 0.3 })
    const loud = levelsFor({ ...base, narrationLevel: 1.5 })
    expect(quiet.narration).toBeCloseTo(0.3, 5)
    expect(loud.narration).toBeCloseTo(1.5, 5)
    expect(quiet.video).toBe(loud.video)
  })

  it('moves the video without moving the voice', () => {
    const a = levelsFor({ ...base, duckLevel: 0.1 })
    const b = levelsFor({ ...base, duckLevel: 0.6 })
    expect(a.video).toBeCloseTo(0.1, 5)
    expect(b.video).toBeCloseTo(0.6, 5)
    expect(a.narration).toBe(b.narration)
  })

  it('scales both with the player volume', () => {
    const half = levelsFor({ ...base, master: 0.5 })
    expect(half.narration).toBeCloseTo(0.25, 5)
    expect(half.video).toBeCloseTo(0.1, 5)
  })

  it('leaves the video alone when nothing is being spoken', () => {
    // Ducking a video no voice is talking over would just make it quiet.
    expect(levelsFor({ ...base, narrating: false })).toEqual({
      narration: 0,
      video: 1,
    })
  })

  it('silences both when muted', () => {
    // Mute has to reach the narration bus too: it is a separate output, and
    // muting the video element alone leaves the voice talking.
    expect(levelsFor({ ...base, muted: true })).toEqual({ narration: 0, video: 0 })
  })

  it('allows the voice above full', () => {
    // TTS is quieter than film audio; a ceiling of 1.0 leaves some viewers
    // unable to hear it. A limiter follows this in the graph.
    expect(levelsFor({ ...base, narrationLevel: 3 }).narration).toBeCloseTo(3, 5)
  })
})
