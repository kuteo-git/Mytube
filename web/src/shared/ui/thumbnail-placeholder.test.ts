import { describe, expect, it } from 'vitest'
import { isMissingThumbnail } from './thumbnail-placeholder'

const hq = 'https://i.ytimg.com/vi/hlExoWglU8s/hqdefault.jpg'

describe('isMissingThumbnail', () => {
  it('recognises the tile YouTube serves with a 404', () => {
    // Measured: 404, 1097 bytes, 120x90, and decodable — so the browser fires
    // load rather than error and the ordinary fallback never runs.
    expect(isMissingThumbnail(hq, 120, 90)).toBe(true)
  })

  it('accepts a real hqdefault', () => {
    expect(isMissingThumbnail(hq, 480, 360)).toBe(false)
  })

  it('leaves default.jpg alone at its own size', () => {
    // 120x90 is what default.jpg is meant to be. Asking for it and getting it
    // is not a failure; asking for hqdefault and getting it is.
    expect(
      isMissingThumbnail('https://i.ytimg.com/vi/abc/default.jpg', 120, 90),
    ).toBe(false)
  })

  it('says nothing about images that are not YouTube thumbnails', () => {
    expect(isMissingThumbnail('/media/abc/thumb.jpg', 120, 90)).toBe(false)
  })

  it('is not fooled by a size that is merely small', () => {
    expect(isMissingThumbnail(hq, 121, 90)).toBe(false)
    expect(isMissingThumbnail(hq, 120, 91)).toBe(false)
  })
})
