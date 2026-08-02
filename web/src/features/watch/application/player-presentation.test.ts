import { afterEach, describe, expect, it, vi } from 'vitest'
import { canGoFullscreen, canUsePiP, enterPiP, goFullscreen } from './player-presentation'

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

describe('goFullscreen', () => {
  it('prefers the standard method when it is available', () => {
    setDocumentFlag('fullscreenEnabled', true)
    const video = document.createElement('video')
    const standard = vi.fn(async () => {})
    const webkit = vi.fn()
    Object.assign(video, { requestFullscreen: standard, webkitEnterFullscreen: webkit })

    goFullscreen(video)

    expect(standard).toHaveBeenCalled()
    expect(webkit).not.toHaveBeenCalled()
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
