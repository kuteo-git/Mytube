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

  it('stops once both the media and the subtitles are in', () => {
    expect(videoPollInterval(READY, 0)).toBe(false)
  })

  // A download that failed is never going to become READY, and a page that
  // polls forever on it is the same problem in a different coat.
  it('stops on a failed download that has subtitles', () => {
    expect(videoPollInterval({ mediaState: 'FAILED', subtitles: [{}] }, 0))
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
