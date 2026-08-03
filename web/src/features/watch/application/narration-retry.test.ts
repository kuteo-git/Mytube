import { describe, expect, it } from 'vitest'
import { retryDelayMs, worthRetrying } from './narration-retry'

describe('retryDelayMs', () => {
  it('does not wait before the first attempt', () => {
    expect(retryDelayMs(0)).toBe(0)
  })

  it('waits briefly, then longer', () => {
    // Most failures are momentary, so the first retry should be quick; the
    // second waits long enough to be a retry rather than a repeat.
    expect(retryDelayMs(1)).toBe(1_000)
    expect(retryDelayMs(2)).toBe(3_000)
  })
})

describe('worthRetrying', () => {
  it('retries a request that failed to get through', () => {
    expect(worthRetrying({ aborted: false, error: 'Failed to fetch' })).toBe(true)
    expect(worthRetrying({ aborted: false, error: 'server returned 502' })).toBe(true)
  })

  it('does not retry a batch that succeeded', () => {
    expect(worthRetrying({ aborted: false, error: '' })).toBe(false)
  })

  it('does not retry an abort', () => {
    // The viewer left the video and the request was cancelled on purpose.
    // Retrying is work for a page nobody is on.
    expect(worthRetrying({ aborted: true, error: 'The user aborted a request' })).toBe(false)
  })

  it('does not retry a model that answered with nothing', () => {
    // It replied, so the trip worked. The same prompt gets the same answer, and
    // asking again is a round trip spent to be told the same thing.
    expect(worthRetrying({ aborted: false, error: 'got 0/15 lines, all empty' })).toBe(false)
  })
})
