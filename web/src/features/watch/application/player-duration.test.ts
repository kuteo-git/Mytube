import { describe, expect, it } from 'vitest'

import { playbackDuration } from './player-duration'

/**
 * The reported fault: a video whose time read `0:14 / 0:00`.
 *
 * Its catalogue row says 0 — it arrived through a flat listing, which carries
 * no duration — while its HLS playlist is `EXT-X-PLAYLIST-TYPE:VOD` with an
 * `ENDLIST` and 3,166 segments summing to 16,254 seconds. The length was there
 * the whole time and the player refused to read it, because the rule said "only
 * trust the element on the local file".
 *
 * That rule was written for the muxed tier, whose header genuinely does not
 * state a total. That tier is gone; the rule outlived it.
 */
describe('how long the video is', () => {
  it('reads the length the stream states, even before the file is on disk', () => {
    expect(
      playbackDuration({ elementDuration: 16254, catalogueDuration: 0 }),
    ).toBe(16254)
  })

  it('falls back to the catalogue until metadata arrives', () => {
    expect(
      playbackDuration({ elementDuration: 0, catalogueDuration: 641 }),
    ).toBe(641)
  })

  it('prefers the element over a catalogue that disagrees', () => {
    // What is playing is the authority on how long it is.
    expect(
      playbackDuration({ elementDuration: 1270, catalogueDuration: 999 }),
    ).toBe(1270)
  })

  it('has nothing to show when neither source knows yet', () => {
    expect(playbackDuration({ elementDuration: 0, catalogueDuration: 0 })).toBe(0)
  })

  describe('a broadcast', () => {
    it('is drawn against its window, not against any total', () => {
      expect(
        playbackDuration({
          elementDuration: 0,
          catalogueDuration: 0,
          isLive: true,
          liveWindow: { start: 0, end: 3605 },
        }),
      ).toBe(3605)
    })

    it('shows nothing until the window is known, rather than a full bar', () => {
      expect(
        playbackDuration({ elementDuration: 0, catalogueDuration: 0, isLive: true }),
      ).toBe(0)
    })
  })

  /**
   * An offset is always zero today. The parameter is kept so that
   * reintroducing one cannot quietly draw a half-watched film as barely begun:
   * an element opened at ten minutes reports only what remains.
   */
  it('distrusts the element when the stream does not start at zero', () => {
    expect(
      playbackDuration({ elementDuration: 300, catalogueDuration: 900, offsetSeconds: 600 }),
    ).toBe(900)
  })
})
