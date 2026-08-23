import { describe, expect, it } from 'vitest'
import { MAX_POLLS, VIDEO_POLL_MS, videoPollInterval } from './video-poll'

const READY = { mediaState: 'READY', subtitles: [{ language: 'en' }] }

describe('videoPollInterval', () => {
  it('keeps asking while the media is still downloading', () => {
    expect(videoPollInterval({ mediaState: 'DOWNLOADING', subtitles: [] }, 0))
      .toBe(VIDEO_POLL_MS)
  })

  // The regression this exists for. A short video finishes downloading before
  // FetchSubtitles publishes, and the old rule stopped polling right there.
  it('keeps asking when the file is ready but no subtitles have arrived', () => {
    expect(videoPollInterval({ mediaState: 'READY', subtitles: [] }, 0))
      .toBe(VIDEO_POLL_MS)
  })

  it('stops once the subtitles are in', () => {
    expect(videoPollInterval(READY, 0)).toBe(false)
  })

  // A download that failed is never going to become READY, and a page that
  // polls forever on it is the same problem in a different coat.
  it('stops on a failed download that has subtitles', () => {
    expect(videoPollInterval({ mediaState: 'FAILED', subtitles: [{}] }, 0))
      .toBe(false)
  })

  // The regression this exists for. With caching switched off nothing is ever
  // downloaded, so a row left at DOWNLOADING stays there for good and a rule
  // waiting for READY can never be satisfied. Both videos this was found on had
  // their subtitles on disk and their state stuck days earlier: the page ran the
  // full forty polls — forty full metadata fetches — and then stopped for good.
  it('stops on a row stuck at DOWNLOADING once its subtitles are there', () => {
    expect(videoPollInterval({ mediaState: 'DOWNLOADING', subtitles: [{}] }, 0))
      .toBe(false)
  })

  it('gives up rather than polling a video that simply has no subtitles', () => {
    const noSubs = { mediaState: 'READY', subtitles: [] }
    expect(videoPollInterval(noSubs, MAX_POLLS - 1)).toBe(VIDEO_POLL_MS)
    expect(videoPollInterval(noSubs, MAX_POLLS)).toBe(false)
  })

  it('polls while the first response is still outstanding', () => {
    expect(videoPollInterval(undefined, 0)).toBe(VIDEO_POLL_MS)
  })
})
