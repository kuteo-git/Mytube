import { describe, expect, it } from 'vitest'
import { STALL_SECONDS, hasStalled } from './narration-watchdog'

const healthy = {
  wanted: true,
  playing: true,
  scheduled: 0,
  cursor: 5,
  cursorAtPlayhead: 5,
  silentFor: 0,
}

describe('when narration has quietly stopped', () => {
  it('is stalled after long enough with nothing said and nothing queued', () => {
    expect(hasStalled({ ...healthy, silentFor: STALL_SECONDS })).toBe(true)
  })

  it('is not stalled while a clip is still on the timeline', () => {
    // A long line is not a stall. Something is queued, so it is working.
    expect(hasStalled({ ...healthy, scheduled: 1, silentFor: 60 })).toBe(false)
  })

  it('is not stalled while the next line is simply not due yet', () => {
    // The ordinary state between two subtitles: the cursor is ahead of the
    // playhead because there is a line still to come. Restarting here would
    // reset narration several times a minute for doing exactly the right thing
    // — and a watchdog that fires during normal operation is worse than none.
    expect(
      hasStalled({ ...healthy, cursor: 9, cursorAtPlayhead: 5, silentFor: 60 }),
    ).toBe(false)
  })

  it('is not stalled just short of the threshold', () => {
    expect(hasStalled({ ...healthy, silentFor: STALL_SECONDS - 0.1 })).toBe(false)
  })

  it('says nothing about a video that is paused', () => {
    // Silence while stopped is not a fault, and restarting there would place
    // clips against a playhead that is not moving.
    expect(hasStalled({ ...healthy, playing: false, silentFor: 600 })).toBe(false)
  })

  it('says nothing when nobody asked to be read to', () => {
    expect(hasStalled({ ...healthy, wanted: false, silentFor: 600 })).toBe(false)
  })

  it('is not tripped by a seek backwards', () => {
    // The figure goes negative when the playhead moves back behind where the
    // last clip was placed. Narration rebases it on every jump, and this is the
    // guarantee that a missed rebase cannot fire the watchdog by accident.
    expect(hasStalled({ ...healthy, silentFor: -120 })).toBe(false)
  })
})
