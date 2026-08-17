import { describe, expect, it } from 'vitest'
import { remuxLead } from './remux-lead'

/**
 * The lead is how far ahead of the viewer a muxed stream is opened, and it used
 * to be a constant. A constant cannot be right: preparation was measured at
 * about 4.4s for a five-minute video and 10.8s for a seventy-eight-minute one,
 * so the twenty seconds that covered the long case spent thirty extra seconds of
 * 360p on the short one — and on the videos where it still ran late, every climb
 * was late, three late climbs turned 1080p off, and pinning it by hand was the
 * only way to see full resolution at all.
 */
describe('how far ahead the next muxed stream is opened', () => {
  it('guesses only until there is a measurement', () => {
    // Nothing known yet: the opening guesses, and the shorter one after a seek
    // because by then the stream has been opened at least once.
    expect(remuxLead(undefined, false)).toBe(20)
    expect(remuxLead(undefined, true)).toBe(5)
  })

  it('uses what the last one actually cost, plus a margin', () => {
    // A stream that took 4.4s once may take 6s next time; the margin is what
    // keeps that from landing behind the viewer, where it cannot be used at all.
    expect(remuxLead(4400, false)).toBeCloseTo(8.4, 3)
    expect(remuxLead(10800, false)).toBeCloseTo(14.8, 3)
  })

  it('stops paying attention to a freak measurement', () => {
    // A measurement of nothing is not evidence that no lead is needed, and one
    // of two minutes is not a reason to show 360p for two minutes.
    expect(remuxLead(0, false)).toBe(5)
    expect(remuxLead(120_000, false)).toBe(30)
  })

  it('lets a measurement override the post-seek guess too', () => {
    // The post-seek guess exists because reopening an already-resolved stream is
    // cheaper than the first open. A real measurement of that same case is
    // better than the guess about it.
    expect(remuxLead(3000, true)).toBeCloseTo(7, 3)
  })
})
