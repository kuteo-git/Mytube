import { afterEach, describe, expect, it } from 'vitest'
import { bypassesWebAudio } from './hls-source'

/**
 * Where the audio graph carries nothing, so the player must stop using it.
 *
 * The rule is a measurement, not a guess. See `bypassesWebAudio` for the three
 * readings; the short version is that on iOS an HLS source never reaches Web
 * Audio, through hls.js as much as natively, while an ordinary file does.
 */

const vendor = Object.getOwnPropertyDescriptor(window.navigator, 'vendor')
const agent = Object.getOwnPropertyDescriptor(window.navigator, 'userAgent')

function pretend(v: string, ua: string, mms: boolean) {
  Object.defineProperty(window.navigator, 'vendor', { configurable: true, get: () => v })
  Object.defineProperty(window.navigator, 'userAgent', { configurable: true, get: () => ua })
  if (mms) (window as unknown as Record<string, unknown>).ManagedMediaSource = function () {}
  else delete (window as unknown as Record<string, unknown>).ManagedMediaSource
}

const IPHONE = ['Apple Computer, Inc.', 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X)', true] as const
const CHROME = ['Google Inc.', 'Mozilla/5.0 (Macintosh) Chrome/140.0.0.0 Safari/537.36', false] as const

afterEach(() => {
  if (vendor) Object.defineProperty(window.navigator, 'vendor', vendor)
  if (agent) Object.defineProperty(window.navigator, 'userAgent', agent)
  delete (window as unknown as Record<string, unknown>).ManagedMediaSource
})

describe('bypassesWebAudio', () => {
  it('is true for a playlist on the device that will not carry it', () => {
    pretend(...IPHONE)
    expect(bypassesWebAudio('/api/videos/a/hls/master.m3u8')).toBe(true)
  })

  it('is false for the file on disk, which the same device carries fine', () => {
    pretend(...IPHONE)
    // 0.0806 against 0.0000, on one page minutes apart. This is the reading
    // that makes the rule about HLS rather than about iOS.
    expect(bypassesWebAudio('/media/a/1080p.mp4')).toBe(false)
    expect(bypassesWebAudio(undefined)).toBe(false)
  })

  it('is false on a browser that puts HLS through the graph', () => {
    pretend(...CHROME)
    // Desktop Chrome, hls.js, same page: 0.1378. The equaliser works there and
    // must not be switched off on the strength of another platform's fault.
    expect(bypassesWebAudio('/api/videos/a/hls/master.m3u8')).toBe(false)
  })
})
