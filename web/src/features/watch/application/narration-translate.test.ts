import { describe, expect, it } from 'vitest'
import { applyAfterTranslation, prepForTranslation } from './narration-translate'

/**
 * These used to be tested against a copy of the replacements written inside the
 * test file, which is to say they were not tested. The rules now live in a
 * module and this runs them.
 */

const roundTrip = (text: string) => applyAfterTranslation(prepForTranslation(text))

describe('prepForTranslation', () => {
  it('marks the plain pronoun', () => {
    expect(prepForTranslation('you are here')).toBe('XXXX are here')
  })

  it('keeps the verb out of a contraction', () => {
    // Replacing "you're" whole would take the "are" with it and lose the tense.
    expect(prepForTranslation("you're late")).toBe('XXXX are late')
    expect(prepForTranslation("you'll see")).toBe('XXXX will see')
    expect(prepForTranslation("you've done it")).toBe('XXXX have done it')
    expect(prepForTranslation("you'd know")).toBe('XXXX would know')
  })

  it('handles the plural greeting', () => {
    expect(prepForTranslation("y'all ready")).toBe('XXXX all ready')
  })

  it('marks possessive and reflexive forms separately', () => {
    expect(prepForTranslation('your book')).toBe('YYYY book')
    expect(prepForTranslation('it is yours')).toBe('it is YYYY')
    expect(prepForTranslation('do it yourself')).toBe('do it YYYY')
    expect(prepForTranslation('help yourselves')).toBe('help YYYY')
  })

  it('leaves words that merely start the same alone', () => {
    // The reason for the word boundaries.
    expect(prepForTranslation('a young youth in Yourktown')).toBe('a young youth in Yourktown')
  })

  it('is case-insensitive but does not care about the case it writes', () => {
    expect(prepForTranslation('You and YOUR friend')).toBe('XXXX and YYYY friend')
  })
})

describe('applyAfterTranslation', () => {
  it('puts the Vietnamese pronouns back', () => {
    expect(applyAfterTranslation('XXXX là ai')).toBe('bạn là ai')
    expect(applyAfterTranslation('sách YYYY')).toBe('sách của bạn')
  })

  it('survives a model that changed the marker case', () => {
    expect(applyAfterTranslation('xxxx là ai')).toBe('bạn là ai')
  })
})

describe('round trip', () => {
  it('turns every addressee form into bạn or của bạn', () => {
    // Standing in for a translator that passes the markers through untouched,
    // which is the whole reason for using markers.
    expect(roundTrip("you're reading your book yourself")).toBe(
      'bạn are reading của bạn book của bạn',
    )
  })

  it('leaves text with no pronouns untouched', () => {
    const text = 'the weather is fine today'
    expect(roundTrip(text)).toBe(text)
  })
})
