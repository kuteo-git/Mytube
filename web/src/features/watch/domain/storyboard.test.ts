import { describe, expect, it } from 'vitest'

import { storyboardFrame, type Storyboard } from './storyboard'

/**
 * The geometry the running gateway actually answered with, not an invented one.
 *
 * Big Buck Bunny: 635 seconds, six sheets of a 5x5 grid of 160x90 stills, each
 * sheet covering 124.02 seconds.
 */
const bunny: Storyboard = {
  tileWidth: 160,
  tileHeight: 90,
  rows: 5,
  columns: 5,
  intervalSeconds: 124.02 / 25,
  sprites: ['v/storyboard/0.webp', '1', '2', '3', '4', 'v/storyboard/5.webp'],
}

describe('storyboardFrame', () => {
  it('puts the opening moment at the top left of the first sheet', () => {
    expect(storyboardFrame(bunny, 0)).toEqual({ sprite: 'v/storyboard/0.webp', x: 0, y: 0 })
  })

  it('walks a row before it starts the next one', () => {
    // Still 1 is to the right of still 0, not beneath it — the sheets are laid
    // out row-major, and reading them column-major draws a moment from five
    // stills away for the whole video.
    expect(storyboardFrame(bunny, bunny.intervalSeconds)).toMatchObject({ x: 160, y: 0 })
    expect(storyboardFrame(bunny, bunny.intervalSeconds * 4)).toMatchObject({ x: 640, y: 0 })
    expect(storyboardFrame(bunny, bunny.intervalSeconds * 5)).toMatchObject({ x: 0, y: 90 })
  })

  it('crosses into the next sheet after the grid is full', () => {
    expect(storyboardFrame(bunny, bunny.intervalSeconds * 24)).toMatchObject({
      sprite: 'v/storyboard/0.webp',
      x: 640,
      y: 360,
    })
    expect(storyboardFrame(bunny, bunny.intervalSeconds * 25)).toMatchObject({
      sprite: '1',
      x: 0,
      y: 0,
    })
  })

  it('still draws a still at the very end of the video', () => {
    // 635s is past the 128 stills the video actually has, and lands in the
    // partly filled last sheet. Without the clamp the end of every bar has no
    // picture, which is exactly where somebody looking for the last scene
    // scrubs to.
    const frame = storyboardFrame(bunny, 635)
    expect(frame?.sprite).toBe('v/storyboard/5.webp')
  })

  it('never reaches past the last sheet, however far it is asked', () => {
    const frame = storyboardFrame(bunny, 99_999)
    expect(frame).toEqual({ sprite: 'v/storyboard/5.webp', x: 640, y: 360 })
  })

  it('treats a moment before the beginning as the beginning', () => {
    // A player reports a negative position before it has loaded, on some
    // platforms, which is not a place in a video.
    expect(storyboardFrame(bunny, -5)).toEqual({ sprite: 'v/storyboard/0.webp', x: 0, y: 0 })
  })

  it('draws nothing rather than the opening shot when there is no storyboard', () => {
    expect(storyboardFrame(undefined, 10)).toBeUndefined()
    expect(storyboardFrame({ ...bunny, sprites: [] }, 10)).toBeUndefined()
    expect(storyboardFrame({ ...bunny, intervalSeconds: 0 }, 10)).toBeUndefined()
    expect(storyboardFrame({ ...bunny, rows: 0 }, 10)).toBeUndefined()
    expect(storyboardFrame(bunny, Number.NaN)).toBeUndefined()
  })
})
