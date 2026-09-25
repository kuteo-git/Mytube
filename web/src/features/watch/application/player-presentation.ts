import { useEffect, useState } from 'react'
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

/**
 * Fills the screen with the player.
 *
 * `frame` is the element around the picture — the one that also holds this
 * app's controls — and it is what the standard API is given. Handing it the
 * `<video>` instead has two costs, and both were reported:
 *
 *  - Chrome gives a fullscreened video element a click-to-play/pause gesture of
 *    its own, fired ~300ms after the click so it can tell one tap from two. A
 *    tap on the picture therefore paused through this app and was started again
 *    by the browser a moment later. Measured: 6 of 6 attempts in full screen
 *    against 0 of 6 windowed.
 *  - The controls are painted in the page, and a fullscreened video covers the
 *    page. So there were none.
 *
 * The webkit path still takes the video, because Apple's player is opened by
 * the element holding the media and a `<div>` opens nothing.
 */
export function goFullscreen(video: HTMLVideoElement | null, frame?: Element | null): void {
  if (!video) return
  const webkit = video as WebkitVideo

  // The webkit method first, wherever it exists — which in practice means an
  // iPhone, and only an iPhone.
  //
  // Preferring the standard call looked like the tidier rule and was the wrong
  // way round. Safari now reports the Fullscreen API as available on iPhone, and
  // what it gives is the element expanded within the page: no rotation to
  // landscape, and no system player to hand playback back from on the way out.
  // `webkitEnterFullscreen` is the one that opens Apple's own player, which is
  // what a phone is expected to do and what rotates.
  //
  // The price is that Apple's controls take the screen, so the quality menu and
  // the narration switch are out of reach until the viewer comes back.
  if (typeof webkit.webkitEnterFullscreen === 'function') {
    webkit.webkitEnterFullscreen()
    return
  }

  const target = frame ?? video
  if (typeof target.requestFullscreen === 'function') {
    void target.requestFullscreen().catch(() => undefined)
  }
}

/**
 * The way back out.
 *
 * Asked of the document rather than of an element, which is what the API takes
 * — and the honest shape anyway: by the time somebody leaves, the element that
 * went in may have been replaced under them.
 *
 * It exists because the frame is what fills the screen now. While the `<video>`
 * was the fullscreen element, this app's controls were painted in the page
 * behind it and nobody could see the button; with the frame there, the button
 * is on screen and a press that did nothing would be the one thing §5 of the
 * charter forbids outright.
 *
 * Guarded on `fullscreenElement`: Chrome rejects the call outside full screen,
 * and an unhandled rejection inside a click handler is a console error on an
 * ordinary press.
 */
export function leaveFullscreen(): void {
  if (typeof document === 'undefined') return
  if (!document.fullscreenElement) return
  if (typeof document.exitFullscreen === 'function') {
    void document.exitFullscreen().catch(() => undefined)
  }
}

export function canUsePiP(): boolean {
  if (typeof document === 'undefined') return false
  if (document.pictureInPictureEnabled) return true
  return hasVideoMethod('webkitSetPresentationMode')
}

export function enterPiP(video: HTMLVideoElement | null): void {
  if (!video) return
  const webkit = video as WebkitVideo

  // Same order and the same reason as full screen: where Safari's own method
  // exists it is the one that works, whatever the standard flag claims.
  if (typeof webkit.webkitSetPresentationMode === 'function') {
    // iOS will not float a video that is not running, and says so by doing
    // nothing at all. Starting it first is inside the same gesture as the
    // press, so the autoplay policy has no objection either.
    if (video.paused) void video.play().catch(() => undefined)
    webkit.webkitSetPresentationMode('picture-in-picture')
    return
  }

  if (typeof video.requestPictureInPicture === 'function') {
    void video.requestPictureInPicture().catch(() => undefined)
  }
}

/**
 * Whether this particular video can float, asked of the element itself.
 *
 * `webkitSupportsPresentationMode` is the only honest answer available on iOS —
 * the method existing on the prototype says the browser knows the idea, not that
 * this video qualifies. It needs a real element, so callers ask once the element
 * exists rather than while deciding what to render, and keep the answer.
 */
export function videoSupportsPiP(video: HTMLVideoElement | null): boolean {
  if (!video) return false
  const webkit = video as WebkitVideo
  if (typeof webkit.webkitSupportsPresentationMode === 'function') {
    return webkit.webkitSupportsPresentationMode('picture-in-picture')
  }
  return typeof document !== 'undefined' && Boolean(document.pictureInPictureEnabled)
}

/**
 * Whether anything is currently filling the screen.
 *
 * A subscription rather than a read, because the way out is not only this
 * app's button: Escape and the browser's own chrome both leave, and a button
 * whose icon was read once would then be showing the wrong way.
 */
export function useIsFullscreen(): boolean {
  const [full, setFull] = useState(
    () => typeof document !== 'undefined' && !!document.fullscreenElement,
  )
  useEffect(() => {
    if (typeof document === 'undefined') return
    const read = () => setFull(!!document.fullscreenElement)
    read()
    document.addEventListener('fullscreenchange', read)
    // Safari has never sent the unprefixed event.
    document.addEventListener('webkitfullscreenchange', read)
    return () => {
      document.removeEventListener('fullscreenchange', read)
      document.removeEventListener('webkitfullscreenchange', read)
    }
  }, [])
  return full
}

/**
 * Where a menu opened from the player has to be rendered.
 *
 * `document.body` while nothing fills the screen — the player frame clips its
 * own contents with `overflow-hidden`, to keep the picture's rounded corners,
 * so a menu left inside it is cut off.
 *
 * And the element in full screen, because only that element and its descendants
 * are drawn there. A menu portalled to the body while the player frame fills
 * the screen opens perfectly well and is painted underneath it, which is a
 * button that appears to do nothing. Measured before this existed: the panel
 * was in the DOM at 1052,596 with `parent: BODY`, and invisible.
 *
 * The frame's `overflow-hidden` does not clip it: the menu is `position: fixed`
 * and that frame is a plain positioned box, so it is not the menu's containing
 * block.
 */
export function menuHost(fullscreenElement: Element | null): Element {
  return fullscreenElement ?? document.body
}
