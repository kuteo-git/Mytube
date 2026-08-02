import { beforeEach, describe, expect, it } from 'vitest'
import { forgetLastWatched, readLastWatched, rememberLastWatched } from './last-watched'

const HOUR = 60 * 60 * 1000
const DAY = 24 * HOUR

beforeEach(() => forgetLastWatched())

describe('rememberLastWatched', () => {
  it('keeps what was playing and where it had got to', () => {
    rememberLastWatched('abc', 42.7, 300, 1000)
    expect(readLastWatched(1000)).toEqual({
      videoId: 'abc',
      positionSeconds: 42,
      savedAt: 1000,
    })
  })

  it('forgets a video watched to the end', () => {
    // Offering it back would mean a corner window that plays two seconds of
    // credits and stops.
    rememberLastWatched('abc', 10, 300, 1000)
    rememberLastWatched('abc', 299, 300, 2000)
    expect(readLastWatched(2000)).toBeNull()
  })

  it('keeps one that is merely well advanced', () => {
    rememberLastWatched('abc', 200, 300, 1000)
    expect(readLastWatched(1000)?.videoId).toBe('abc')
  })

  it('ignores an empty id', () => {
    rememberLastWatched('', 10, 300, 1000)
    expect(readLastWatched(1000)).toBeNull()
  })

  it('does not mind an unknown duration', () => {
    rememberLastWatched('abc', 10, 0, 1000)
    expect(readLastWatched(1000)?.videoId).toBe('abc')
  })
})

describe('readLastWatched', () => {
  it('offers back something from an hour ago', () => {
    rememberLastWatched('abc', 42, 300, 0)
    expect(readLastWatched(HOUR)?.videoId).toBe('abc')
  })

  it('lets go of something from a fortnight ago', () => {
    // Coming back after a holiday to whatever was on before it is not a
    // courtesy; the intent this records has expired.
    rememberLastWatched('abc', 42, 300, 0)
    expect(readLastWatched(14 * DAY)).toBeNull()
  })

  it('survives a corrupted entry', () => {
    window.localStorage.setItem('yt-last-watched', 'not json')
    expect(readLastWatched()).toBeNull()
    // And clears it, so it is not re-parsed on every visit forever.
    expect(window.localStorage.getItem('yt-last-watched')).toBeNull()
  })

  it('rejects an entry with no video in it', () => {
    window.localStorage.setItem('yt-last-watched', JSON.stringify({ savedAt: Date.now() }))
    expect(readLastWatched()).toBeNull()
  })

  it('is nothing at all when nothing was watched', () => {
    expect(readLastWatched()).toBeNull()
  })
})

describe('forgetLastWatched', () => {
  it('leaves nothing to offer', () => {
    rememberLastWatched('abc', 42, 300, 1000)
    forgetLastWatched()
    expect(readLastWatched(1000)).toBeNull()
  })
})
