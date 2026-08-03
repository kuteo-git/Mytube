import { describe, expect, it } from 'vitest'
import { estimateEtaSeconds, formatDuration } from './narration-eta'

describe('estimateEtaSeconds', () => {
  it('measures the rate from cues actually translated, not cues seeded', () => {
    // 100 of 150 came straight off disk in no time. Counting those would say
    // the whole video finishes in a moment, and the bar would sit at "almost
    // done" for minutes.
    const eta = estimateEtaSeconds({
      done: 110,
      total: 150,
      baseline: 100,
      elapsedMs: 10_000,
    })
    // 10 cues in 10s -> 1/s -> 40 left.
    expect(eta).toBe(40)
  })

  it('has nothing to say before anything has been translated', () => {
    expect(
      estimateEtaSeconds({ done: 5, total: 150, baseline: 5, elapsedMs: 3000 }),
    ).toBeNull()
  })

  it('says nothing on the strength of the opening batch alone', () => {
    // Three cues is the whole first batch, and it may have carried the model
    // load — 31.5s cold against 3s warm. A rate from it said nine minutes where
    // the truth was three, and an estimate that corrects itself downwards by a
    // factor of three is worse than one that arrives a batch later.
    expect(
      estimateEtaSeconds({ done: 3, total: 150, baseline: 0, elapsedMs: 10_800 }),
    ).toBeNull()
  })

  it('quotes a figure once a full batch has gone through', () => {
    // 18 cues in 26.7s, both measured today.
    const eta = estimateEtaSeconds({
      done: 18,
      total: 150,
      baseline: 0,
      elapsedMs: 26_700,
    })
    expect(eta).not.toBeNull()
    expect(eta!).toBeGreaterThan(150)
    expect(eta!).toBeLessThan(250)
  })

  it('is zero once everything is done', () => {
    expect(
      estimateEtaSeconds({ done: 150, total: 150, baseline: 0, elapsedMs: 60_000 }),
    ).toBe(0)
  })

  it('ignores a clock that has not moved', () => {
    expect(
      estimateEtaSeconds({ done: 50, total: 150, baseline: 0, elapsedMs: 0 }),
    ).toBeNull()
  })
})

describe('formatDuration', () => {
  it('reads in seconds under a minute', () => {
    expect(formatDuration(0)).toBe('vài giây')
    expect(formatDuration(8)).toBe('khoảng 10 giây')
    expect(formatDuration(44)).toBe('khoảng 45 giây')
  })

  it('reads in minutes above one', () => {
    expect(formatDuration(60)).toBe('khoảng 1 phút')
    expect(formatDuration(150)).toBe('khoảng 3 phút')
  })

  it('rounds to a coarse figure past ten minutes', () => {
    // Precision no one can act on invites people to watch it tick.
    expect(formatDuration(1000)).toBe('khoảng 15 phút')
    expect(formatDuration(3000)).toBe('khoảng 50 phút')
  })
})
