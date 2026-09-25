import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  canGoFullscreen,
  canUsePiP,
  enterPiP,
  goFullscreen,
  leaveFullscreen,
  menuHost,
  videoSupportsPiP,
} from './player-presentation'

/**
 * Asking each browser in the words it understands.
 *
 * iPhone Safari has neither standard method. Both buttons called them anyway,
 * with optional chaining, so a missing method looked exactly like one that
 * worked: the button was on screen, it was pressed, and nothing happened and
 * nothing was reported. Which is how it was found — by someone pressing it.
 */

function withVideoMethod(name: string, value: unknown) {
  Object.defineProperty(HTMLVideoElement.prototype, name, {
    configurable: true,
    writable: true,
    value,
  })
  return () => {
    delete (HTMLVideoElement.prototype as unknown as Record<string, unknown>)[name]
  }
}

function setDocumentFlag(name: 'fullscreenEnabled' | 'pictureInPictureEnabled', value: boolean) {
  Object.defineProperty(document, name, { configurable: true, value })
}

afterEach(() => {
  setDocumentFlag('fullscreenEnabled', false)
  setDocumentFlag('pictureInPictureEnabled', false)
})

describe('canGoFullscreen', () => {
  it('is true where the standard API is enabled', () => {
    setDocumentFlag('fullscreenEnabled', true)
    expect(canGoFullscreen()).toBe(true)
  })

  it('is true on a browser that only has the webkit method', () => {
    // An iPhone. The standard flag is false there and always has been.
    setDocumentFlag('fullscreenEnabled', false)
    const undo = withVideoMethod('webkitEnterFullscreen', () => {})
    expect(canGoFullscreen()).toBe(true)
    undo()
  })

  it('is false when neither exists, so no button is drawn', () => {
    setDocumentFlag('fullscreenEnabled', false)
    expect(canGoFullscreen()).toBe(false)
  })
})

describe('leaveFullscreen', () => {
  it('asks the document to exit, not the element', () => {
    // Exiting is a document-level act: whatever is filling the screen, this is
    // the way back. The element that went in may no longer exist by then.
    const exit = vi.fn(async () => {})
    Object.defineProperty(document, 'exitFullscreen', { configurable: true, value: exit })
    Object.defineProperty(document, 'fullscreenElement', {
      configurable: true, value: document.createElement('div'),
    })

    leaveFullscreen()

    expect(exit).toHaveBeenCalled()
  })

  it('does nothing when nothing is filling the screen', () => {
    // Chrome rejects exitFullscreen outside full screen, and an unhandled
    // rejection in a click handler is a console error on an ordinary press.
    const exit = vi.fn(async () => {})
    Object.defineProperty(document, 'exitFullscreen', { configurable: true, value: exit })
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: null })

    leaveFullscreen()

    expect(exit).not.toHaveBeenCalled()
  })
})

describe('goFullscreen', () => {
  it('prefers the webkit method even where the standard one is offered', () => {
    // Safari reports the Fullscreen API on iPhone, and what it gives is the
    // element expanded inside the page: no rotation to landscape, and no system
    // player to hand playback back from on the way out. Preferring the standard
    // call because it is the standard was the wrong way round.
    setDocumentFlag('fullscreenEnabled', true)
    const video = document.createElement('video')
    const standard = vi.fn(async () => {})
    const webkit = vi.fn()
    Object.assign(video, { requestFullscreen: standard, webkitEnterFullscreen: webkit })

    goFullscreen(video)

    expect(webkit).toHaveBeenCalled()
    expect(standard).not.toHaveBeenCalled()
  })

  it('fullscreens the frame around the video, not the video itself', () => {
    // Chrome gives a fullscreened `<video>` element its own click-to-play
    // gesture, and it fires about 300ms after the click to tell a single tap
    // from a double one. So tapping the picture to pause paused it and then the
    // browser started it again — reported as "pause cỡ 0.5sec sau đó nó play
    // tiếp", and measured at 6 attempts out of 6 in full screen against 0 out
    // of 6 windowed. Handing the surrounding element to the Fullscreen API
    // leaves the gesture to this app alone, and it is what puts the player's own
    // controls on screen there at all — a fullscreened video element covers
    // them, because they are painted in the page it is covering.
    setDocumentFlag('fullscreenEnabled', true)
    const video = document.createElement('video')
    const frame = document.createElement('div')
    const onVideo = vi.fn(async () => {})
    const onFrame = vi.fn(async () => {})
    Object.assign(video, { requestFullscreen: onVideo })
    Object.assign(frame, { requestFullscreen: onFrame })

    goFullscreen(video, frame)

    expect(onFrame).toHaveBeenCalled()
    expect(onVideo).not.toHaveBeenCalled()
  })

  it('still hands the video itself to the webkit method', () => {
    // Apple's own player is opened by the element that holds the media, so the
    // iPhone path is unchanged by the frame: passing the frame there would open
    // nothing at all.
    setDocumentFlag('fullscreenEnabled', true)
    const video = document.createElement('video')
    const frame = document.createElement('div')
    const webkit = vi.fn()
    const onFrame = vi.fn(async () => {})
    Object.assign(video, { webkitEnterFullscreen: webkit })
    Object.assign(frame, { requestFullscreen: onFrame })

    goFullscreen(video, frame)

    expect(webkit).toHaveBeenCalled()
    expect(onFrame).not.toHaveBeenCalled()
  })

  it('uses the standard method where there is no webkit one', () => {
    setDocumentFlag('fullscreenEnabled', true)
    const video = document.createElement('video')
    const standard = vi.fn(async () => {})
    Object.assign(video, { requestFullscreen: standard })

    goFullscreen(video)

    expect(standard).toHaveBeenCalled()
  })

  it('falls back to the webkit method on a phone', () => {
    setDocumentFlag('fullscreenEnabled', false)
    const video = document.createElement('video')
    const webkit = vi.fn()
    Object.assign(video, { webkitEnterFullscreen: webkit })

    goFullscreen(video)

    expect(webkit).toHaveBeenCalled()
  })

  it('does nothing at all without an element', () => {
    expect(() => goFullscreen(null)).not.toThrow()
  })
})

describe('canUsePiP', () => {
  it('is true where the standard API is enabled', () => {
    setDocumentFlag('pictureInPictureEnabled', true)
    expect(canUsePiP()).toBe(true)
  })

  it('is true on a browser that only has the webkit presentation mode', () => {
    setDocumentFlag('pictureInPictureEnabled', false)
    const undo = withVideoMethod('webkitSetPresentationMode', () => {})
    expect(canUsePiP()).toBe(true)
    undo()
  })

  it('is false when neither exists', () => {
    setDocumentFlag('pictureInPictureEnabled', false)
    expect(canUsePiP()).toBe(false)
  })
})

describe('enterPiP', () => {
  it('falls back to the webkit presentation mode', () => {
    setDocumentFlag('pictureInPictureEnabled', false)
    const video = document.createElement('video')
    const webkit = vi.fn()
    Object.assign(video, { webkitSetPresentationMode: webkit })

    enterPiP(video)

    expect(webkit).toHaveBeenCalledWith('picture-in-picture')
  })
})

describe('entering picture in picture on a phone', () => {
  it('starts the video first, because iOS will not float a stopped one', () => {
    setDocumentFlag('pictureInPictureEnabled', false)
    const video = document.createElement('video')
    const play = vi.fn(async () => {})
    const webkit = vi.fn()
    Object.defineProperty(video, 'paused', { configurable: true, value: true })
    Object.assign(video, { play, webkitSetPresentationMode: webkit })

    enterPiP(video)

    expect(play).toHaveBeenCalled()
    expect(webkit).toHaveBeenCalledWith('picture-in-picture')
  })

  it('leaves a running video alone', () => {
    setDocumentFlag('pictureInPictureEnabled', false)
    const video = document.createElement('video')
    const play = vi.fn(async () => {})
    Object.defineProperty(video, 'paused', { configurable: true, value: false })
    Object.assign(video, { play, webkitSetPresentationMode: vi.fn() })

    enterPiP(video)

    expect(play).not.toHaveBeenCalled()
  })

  it('does nothing where the method is absent', () => {
    setDocumentFlag('pictureInPictureEnabled', false)
    const video = document.createElement('video')
    const play = vi.fn(async () => {})
    Object.defineProperty(video, 'paused', { configurable: true, value: true })
    Object.assign(video, { play })

    expect(() => enterPiP(video)).not.toThrow()
    // No point starting playback for a floating window that cannot open.
    expect(play).not.toHaveBeenCalled()
  })
})

describe('videoSupportsPiP', () => {
  it('asks the element, which is the only one that knows', () => {
    // The method existing on the prototype says the browser knows the idea, not
    // that this video qualifies.
    const video = document.createElement('video')
    Object.assign(video, {
      webkitSupportsPresentationMode: (mode: string) => mode === 'picture-in-picture',
    })
    expect(videoSupportsPiP(video)).toBe(true)
  })

  it('believes an element that says no', () => {
    const video = document.createElement('video')
    Object.assign(video, { webkitSupportsPresentationMode: () => false })
    expect(videoSupportsPiP(video)).toBe(false)
  })

  it('falls back to the document flag where the element cannot say', () => {
    setDocumentFlag('pictureInPictureEnabled', true)
    expect(videoSupportsPiP(document.createElement('video'))).toBe(true)
  })

  it('is false without an element', () => {
    expect(videoSupportsPiP(null)).toBe(false)
  })
})

describe('entering picture in picture prefers webkit too', () => {
  it('uses the presentation mode even where the standard call exists', () => {
    setDocumentFlag('pictureInPictureEnabled', true)
    const video = document.createElement('video')
    const standard = vi.fn(async () => ({}) as PictureInPictureWindow)
    const webkit = vi.fn()
    Object.defineProperty(video, 'paused', { configurable: true, value: false })
    Object.assign(video, {
      requestPictureInPicture: standard,
      webkitSetPresentationMode: webkit,
    })

    enterPiP(video)

    expect(webkit).toHaveBeenCalledWith('picture-in-picture')
    expect(standard).not.toHaveBeenCalled()
  })
})

describe('capability is read from the prototype, not from an element', () => {
  it('answers before any video has been created', () => {
    // The instance comes from a ref, which is empty on the first render and
    // does not cause another when it fills. A button whose existence depended
    // on it would be missing on the render that decided, with nothing coming
    // back to correct it.
    setDocumentFlag('fullscreenEnabled', false)
    const undo = withVideoMethod('webkitEnterFullscreen', () => {})
    expect(canGoFullscreen()).toBe(true)
    undo()
  })
})

describe('menuHost', () => {
  it('is the body while nothing fills the screen', () => {
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: null })
    expect(menuHost(document.fullscreenElement)).toBe(document.body)
  })

  it('is whatever fills the screen when something does', () => {
    // A menu rendered into the body is painted *under* the fullscreen layer, so
    // it opens and nobody sees it. That is the same dead button the suggestions
    // switch was, one control along.
    const frame = document.createElement('div')
    Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: frame })
    expect(menuHost(document.fullscreenElement)).toBe(frame)
  })
})
