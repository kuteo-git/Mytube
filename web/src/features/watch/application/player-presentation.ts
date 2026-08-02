/**
 * Full screen and picture-in-picture, across browsers that disagree about how
 * to ask.
 *
 * iPhone Safari implements neither standard method. It has never had
 * `Element.requestFullscreen`, and picture-in-picture is reached through
 * `webkitSetPresentationMode` rather than `requestPictureInPicture`. Both
 * buttons therefore did nothing at all on a phone — and did it silently,
 * because the calls were written with optional chaining, so a missing method
 * was indistinguishable from a method that worked.
 *
 * Both webkit methods live on `HTMLVideoElement` specifically, not on any
 * element, which is why these take the video rather than the frame around it.
 */

/** The bits of the webkit video API that are not in the standard typings. */
interface WebkitVideo extends HTMLVideoElement {
  webkitEnterFullscreen?: () => void
  webkitSupportsFullscreen?: boolean
  webkitSetPresentationMode?: (mode: 'picture-in-picture' | 'inline') => void
  webkitSupportsPresentationMode?: (mode: string) => boolean
}

/**
 * Whether this browser can show a video full screen at all.
 *
 * Asked before drawing the button rather than when it is pressed. A control
 * that cannot do anything must not be on screen (CLAUDE.md §5), and a button
 * that looks live and answers to nothing is worse than one that is absent —
 * which is exactly how this was reported.
 */
export function canGoFullscreen(): boolean {
  if (typeof document === 'undefined') return false
  if (document.fullscreenEnabled) return true
  return hasVideoMethod('webkitEnterFullscreen')
}

/**
 * Whether every video element in this browser carries a method.
 *
 * Asked of the prototype rather than of an instance, deliberately. The instance
 * comes from a ref, which is empty on the first render and does not cause
 * another when it fills — so a button whose existence depended on it would be
 * missing on the render that decided, and nothing would come back to correct
 * it. The prototype is there before any element is.
 */
function hasVideoMethod(name: string): boolean {
  return typeof HTMLVideoElement !== 'undefined' && name in HTMLVideoElement.prototype
}

export function goFullscreen(video: HTMLVideoElement | null): void {
  if (!video) return
  if (typeof video.requestFullscreen === 'function' && document.fullscreenEnabled) {
    void video.requestFullscreen().catch(() => undefined)
    return
  }
  // iPhone: hands over to the system player. Its own controls take the screen,
  // so the quality menu and the narration switch are out of reach until the
  // viewer comes back — which is the price of the rotation and the full screen
  // actually being full.
  const webkit = video as WebkitVideo
  webkit.webkitEnterFullscreen?.()
}

export function canUsePiP(): boolean {
  if (typeof document === 'undefined') return false
  if (document.pictureInPictureEnabled) return true
  return hasVideoMethod('webkitSetPresentationMode')
}

export function enterPiP(video: HTMLVideoElement | null): void {
  if (!video) return

  if (
    typeof video.requestPictureInPicture === 'function' &&
    document.pictureInPictureEnabled
  ) {
    void video.requestPictureInPicture().catch(() => undefined)
    return
  }

  const webkit = video as WebkitVideo
  if (typeof webkit.webkitSetPresentationMode !== 'function') return

  // iOS refuses to float a video that is not running, and says so by doing
  // nothing at all. Starting it first is inside the same gesture as the press,
  // so the autoplay policy has no objection either.
  if (video.paused) void video.play().catch(() => undefined)
  webkit.webkitSetPresentationMode('picture-in-picture')
}
