import { describe, expect, it } from 'vitest'
import { MACHINE_LANGUAGE, hasHumanVietnamese } from './subtitle-language'

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
