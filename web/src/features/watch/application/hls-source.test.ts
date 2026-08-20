import { afterEach, describe, expect, it, vi } from 'vitest'
import { canPlayHLSNatively, canPlayHLSWithLibrary, shouldUseHLS } from './hls-source'

/**
 * The capability check that must not repeat the mistake it replaces.
 *
 * `canPlayType('application/vnd.apple.mpegurl')` returns `"maybe"` on Chrome,
 * which cannot play a playlist at all, and `"maybe"` on iOS Safari, which
 * plays one perfectly. Both measured 2026-08-20 on real devices. A whole
 * feature was built on the strength of that string.
 *
 * So these tests describe browsers by what their engine actually has, and the
 * decisive one is the iPhone: no `MediaSource`, so nothing can stand behind a
 * wrong answer there.
 */

const realVendor = Object.getOwnPropertyDescriptor(window.navigator, 'vendor')
const realUA = Object.getOwnPropertyDescriptor(window.navigator, 'userAgent')

function pretend(browser: {
  vendor: string
  userAgent: string
  managedMediaSource: boolean
  mediaSource: boolean
}) {
  Object.defineProperty(window.navigator, 'vendor', {
    configurable: true,
    get: () => browser.vendor,
  })
  Object.defineProperty(window.navigator, 'userAgent', {
    configurable: true,
    get: () => browser.userAgent,
  })
  if (browser.managedMediaSource) {
    ;(window as unknown as Record<string, unknown>).ManagedMediaSource = function () {}
  } else {
    delete (window as unknown as Record<string, unknown>).ManagedMediaSource
  }
  if (browser.mediaSource) {
    ;(window as unknown as Record<string, unknown>).MediaSource = function () {}
  } else {
    delete (window as unknown as Record<string, unknown>).MediaSource
  }
}

/** iOS 18.7, exactly as measured on the household's iPhone. */
const iPhone = {
  vendor: 'Apple Computer, Inc.',
  userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15',
  managedMediaSource: true,
  mediaSource: false,
}

const macSafari = {
  vendor: 'Apple Computer, Inc.',
  userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15',
  managedMediaSource: false,
  mediaSource: true,
}

const macChrome = {
  vendor: 'Google Inc.',
  userAgent:
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36',
  managedMediaSource: false,
  mediaSource: true,
}

const firefox = {
  vendor: '',
  userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:130.0) Gecko/20100101 Firefox/130.0',
  managedMediaSource: false,
  mediaSource: true,
}

afterEach(() => {
  if (realVendor) Object.defineProperty(window.navigator, 'vendor', realVendor)
  if (realUA) Object.defineProperty(window.navigator, 'userAgent', realUA)
  delete (window as unknown as Record<string, unknown>).ManagedMediaSource
  vi.restoreAllMocks()
})

describe('canPlayHLSNatively', () => {
  it('says yes to the iPhone, which is the device that has no second chance', () => {
    pretend(iPhone)
    expect(canPlayHLSNatively()).toBe(true)
    // The point of the whole exercise: nothing can stand behind native HLS here.
    expect(canPlayHLSWithLibrary()).toBe(false)
  })

  it('says yes to Safari on macOS', () => {
    pretend(macSafari)
    expect(canPlayHLSNatively()).toBe(true)
  })

  it('says no to Chrome, which claims "maybe" and then fails with code 4', () => {
    pretend(macChrome)
    expect(canPlayHLSNatively()).toBe(false)
    expect(canPlayHLSWithLibrary()).toBe(true)
  })

  it('says no to Firefox', () => {
    pretend(firefox)
    expect(canPlayHLSNatively()).toBe(false)
    expect(canPlayHLSWithLibrary()).toBe(true)
  })

  /**
   * Chrome on macOS carries "Safari" in its user agent, and older sniffing that
   * looked for that word alone would call it a Safari. The vendor string is
   * what separates them, and the Chromium name check is the belt to that brace.
   */
  it('is not fooled by the word Safari in Chrome’s user agent', () => {
    pretend({ ...macChrome, vendor: 'Apple Computer, Inc.' })
    expect(canPlayHLSNatively()).toBe(false)
  })
})

describe('shouldUseHLS', () => {
  it('opens on HLS only where the browser needs no help', () => {
    pretend(iPhone)
    expect(shouldUseHLS()).toBe(true)

    // Chrome keeps the muxed stream until hls.js is wired in. It works there,
    // which is exactly why the mux looked healthy for a week: every measurement
    // was taken on a desktop.
    pretend(macChrome)
    expect(shouldUseHLS()).toBe(false)
  })
})
