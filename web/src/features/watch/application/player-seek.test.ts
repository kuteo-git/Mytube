import { describe, expect, it } from 'vitest'
import { seekElement } from './player-seek'

/**
 * One door for the playhead.
 *
 * The rule these tests hold has been in CLAUDE.md §4 for weeks — "a stream
 * reported `seekable: false` must never be seeked" — and was enforced at three
 * call sites out of five. The two that forgot did not fail loudly: a browser
 * asked to seek an unindexed stream reports nothing and shows nothing, it just
 * buffers toward a mark it can only reach by streaming there. On a video left
 * at 5:36 that is minutes of blank picture, which reads as "the player is
 * broken" and was fixed, every time, by waiting for the download instead.
 *
 * So the guard moves out of the author's memory and into a function.
 */
describe('seekElement', () => {
  function fakeElement(startAt = 0) {
    return { currentTime: startAt } as HTMLVideoElement
  }

  it('moves the playhead of a stream that can seek', () => {
    const el = fakeElement()

    expect(seekElement(el, { seekable: true }, 42)).toBe('seeked')
    expect(el.currentTime).toBe(42)
  })

  it('refuses a stream that cannot seek, and leaves the playhead alone', () => {
    const el = fakeElement(3)

    expect(seekElement(el, { seekable: false }, 336)).toBe('refused-not-seekable')
    // The number never reaches the element. Writing it is precisely the bug:
    // the browser accepts it and then shows nothing until it has streamed there.
    expect(el.currentTime).toBe(3)
  })

  /**
   * An unknown tier is treated as seekable, deliberately.
   *
   * The tier is undefined only before the first source is chosen, and the local
   * file — the one tier that is always seekable — is the common case there.
   * Refusing would break resuming an ordinary downloaded video to protect a
   * stream that is not playing yet.
   */
  it('allows the seek when no tier is known yet', () => {
    const el = fakeElement()

    expect(seekElement(el, undefined, 12)).toBe('seeked')
    expect(el.currentTime).toBe(12)
  })

  it('says so rather than throwing when there is no element', () => {
    expect(seekElement(null, { seekable: true }, 12)).toBe('no-element')
  })

  /**
   * A negative mark is the caller's arithmetic showing through — an absolute
   * position minus an offset larger than it. Clamped rather than refused: the
   * viewer asked to go somewhere, and the start of the stream is the nearest
   * place to it.
   */
  it('clamps a negative mark to the start', () => {
    const el = fakeElement(9)

    expect(seekElement(el, { seekable: true }, -4)).toBe('seeked')
    expect(el.currentTime).toBe(0)
  })

  /**
   * Some browsers throw on a seek the element is not ready for. That is not a
   * fault the caller can do anything about, and it must not take down a render.
   */
  it('reports a refusal from the element rather than throwing', () => {
    const el = {
      set currentTime(_v: number) {
        throw new Error('InvalidStateError')
      },
      get currentTime() {
        return 0
      },
    } as HTMLVideoElement

    expect(seekElement(el, { seekable: true }, 5)).toBe('refused-by-element')
  })
})
