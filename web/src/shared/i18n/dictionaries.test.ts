import { describe, expect, it } from 'vitest'

import { en } from './en'
import { vi } from './vi'

/**
 * The two dictionaries describe the same set of keys.
 *
 * TypeScript already enforces most of this — `vi` is typed as `Dictionary`, so
 * a missing key does not compile. This covers what the type cannot: an *extra*
 * key in Vietnamese, which is what a rename done on one side only leaves
 * behind, and which nothing else would ever report.
 *
 * It also catches a value left in English by mistake — copy-pasting the English
 * file and translating downward is how the last few keys get forgotten, and
 * they are always at the bottom.
 */

function paths(value: unknown, prefix = ''): string[] {
  if (typeof value !== 'object' || value === null) return [prefix]
  return Object.entries(value).flatMap(([key, child]) =>
    paths(child, prefix ? `${prefix}.${key}` : key),
  )
}

function valueAt(dict: unknown, path: string): string {
  return path.split('.').reduce<never>((node, key) => (node as never)[key], dict as never)
}

describe('the dictionaries', () => {
  it('carry exactly the same keys', () => {
    expect(paths(vi).sort()).toEqual(paths(en).sort())
  })

  /**
   * A Vietnamese value identical to its English one is nearly always a key
   * that was copied and not translated.
   *
   * The exceptions are real and are listed rather than pattern-matched: a
   * proper noun is the same word in both languages, and inventing a Vietnamese
   * spelling for "YouTube" would be worse than leaving it. Every entry here is
   * a decision, so each one has to be made deliberately.
   */
  it('translate everything that is not a proper noun', () => {
    // Empty, and it should stay that way for as long as it can. Every entry
    // is a place the two languages agree, which is a thing to justify rather
    // than to assume — "Tài khoản YouTube" and "Ngôn ngữ" both looked like
    // candidates and both are translated perfectly well.
    const sameInBoth = new Set<string>([
      // A bare number, with no word attached. There is nothing to translate,
      // and it only exists so every slider's readout goes through the same
      // path instead of one of them being a special case in the component.
      'settings.ranking.unit.plain',
    ])

    const untranslated = paths(en).filter(
      (path) => valueAt(en, path) === valueAt(vi, path) && !sameInBoth.has(path),
    )

    expect(untranslated).toEqual([])
  })
})
