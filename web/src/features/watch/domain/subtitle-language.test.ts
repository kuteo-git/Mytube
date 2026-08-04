import { describe, expect, it } from 'vitest'
import {
  MACHINE_LANGUAGE,
  captionsSettled,
  hasHumanVietnamese,
} from './subtitle-language'

describe('hasHumanVietnamese', () => {
  it('sees a Vietnamese track the video shipped with', () => {
    expect(hasHumanVietnamese([{ language: 'en' }, { language: 'vi' }])).toBe(true)
    expect(hasHumanVietnamese([{ language: 'vie' }])).toBe(true)
  })

  // The regression. Counting our own output made the translator hide itself
  // the moment it wrote its first batch.
  it('does not count the translation we just produced', () => {
    expect(
      hasHumanVietnamese([{ language: 'en' }, { language: MACHINE_LANGUAGE }]),
    ).toBe(false)
  })

  it('is false when there is nothing Vietnamese at all', () => {
    expect(hasHumanVietnamese([{ language: 'en' }])).toBe(false)
    expect(hasHumanVietnamese([])).toBe(false)
  })
})

describe('captionsSettled', () => {
  // The bug this exists for: the page renders before captions are published,
  // and an empty list answers "no Vietnamese" for a video that has it.
  it('is false before anything has been published', () => {
    expect(captionsSettled([])).toBe(false)
  })

  it('is true once a published track is present', () => {
    expect(captionsSettled([{ language: 'en' }])).toBe(true)
    expect(captionsSettled([{ language: 'en' }, { language: 'vi' }])).toBe(true)
  })

  // Our own output must not be its own evidence: the pass writes this track, so
  // counting it would let the pass declare the list complete.
  it('does not count the machine translation', () => {
    expect(captionsSettled([{ language: MACHINE_LANGUAGE }])).toBe(false)
  })
})
