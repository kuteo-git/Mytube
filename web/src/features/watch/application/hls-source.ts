/**
 * Whether this browser can play an HLS playlist by itself, and how to give it
 * one.
 *
 * ## Why this is not one line
 *
 * The obvious test is `canPlayType('application/vnd.apple.mpegurl')`, and it
 * lies. Measured 2026-08-20:
 *
 * | | canPlayType | actually plays master.m3u8 |
 * |---|---|---|
 * | Chrome, macOS | `"maybe"` | **no** — `MEDIA_ERR_SRC_NOT_SUPPORTED` |
 * | Safari, iOS 18.7 | `"maybe"` | **yes** — played, duration 641.8s, seeked twice |
 *
 * The same answer from both, and opposite outcomes. `web/public/mse-check.html`
 * asked exactly that question, and the HLS work was built believing "maybe"
 * meant yes on the strength of it.
 *
 * So the question is asked a different way. What actually distinguishes the two
 * is which media pipeline the browser has: Safari has native HLS, and on iOS it
 * has `ManagedMediaSource` but **no** `MediaSource` at all. Chrome has
 * `MediaSource` and no HLS. That is a property of the engine rather than a
 * claim about a MIME type, and it cannot answer "maybe".
 *
 * ## What this means for the iPhone
 *
 * There is nothing behind native HLS on iOS. hls.js needs `MediaSource`, which
 * iOS does not have, so if Safari declines a playlist the device has no way to
 * play a video that is not on disk yet. That is why the codec string is
 * validated on the server before a playlist is written: this is the one path
 * with no fallback under it.
 */

/**
 * Does this browser play HLS on its own?
 *
 * Read from the engine, not from `canPlayType`. A browser with native HLS and
 * no `MediaSource` is Safari on iOS; one with both is Safari on macOS; one with
 * only `MediaSource` is Chrome or Firefox and needs a library.
 *
 * `ManagedMediaSource` is Apple's, and its presence is the sharpest signal
 * available that this is a WebKit that plays HLS. On macOS Safari, where it may
 * be absent, the vendor check catches it: Safari is the only engine that
 * reports "Apple Computer, Inc." while not being Chrome.
 */
export function canPlayHLSNatively(): boolean {
  if (typeof window === 'undefined') return false

  // Apple's own media source. Present on iOS and on recent Safari, and never on
  // Chrome or Firefox.
  if ('ManagedMediaSource' in window) return true

  const nav = window.navigator
  const isAppleVendor = nav.vendor === 'Apple Computer, Inc.'
  // Chrome on macOS reports Safari in its user agent but not Apple as vendor;
  // Chromium engines are excluded by name for the cases where they do.
  const isChromium = /Chrome|Chromium|Edg\//.test(nav.userAgent)
  return isAppleVendor && !isChromium
}

/**
 * Can this browser be *made* to play HLS?
 *
 * Separate from the question above because the answer costs a library. Nothing
 * here loads it — that is the caller's decision and a later change — but the
 * player has to be able to tell "cannot play this at all" from "cannot play it
 * unaided", because only the first means fall back to the muxed stream.
 */
export function canPlayHLSWithLibrary(): boolean {
  return typeof window !== 'undefined' && 'MediaSource' in window
}

/**
 * Should the player open this video on HLS?
 *
 * Only when the browser can do it unaided, for now. Chrome keeps the muxed
 * stream until hls.js is wired in — it works there, which is exactly why it hid
 * this problem for a week: the tier was measured on a desktop where it plays.
 */
export function shouldUseHLS(): boolean {
  return canPlayHLSNatively()
}

/**
 * What the browser reports, for the log.
 *
 * The player prints this once per video. It exists because every wrong turn in
 * this area came from believing a capability check instead of an outcome, and
 * the two are only distinguishable afterwards if the claim was written down.
 */
export function hlsCapabilities(): {
  native: boolean
  withLibrary: boolean
  claim: string
} {
  const claim =
    typeof document === 'undefined'
      ? ''
      : document.createElement('video').canPlayType('application/vnd.apple.mpegurl')
  return {
    native: canPlayHLSNatively(),
    withLibrary: canPlayHLSWithLibrary(),
    // Kept only to be disbelieved: "maybe" from both a browser that plays it
    // and one that does not.
    claim,
  }
}
