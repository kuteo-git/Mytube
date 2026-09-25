import { describe, expect, it } from 'vitest'
import { ALL_CATEGORY } from '@/features/catalog/ui/ChipBar'
import { chipFromSearch, searchForChip } from './home-chip'

// The chip lived in `useState`, so a refresh put Home back to All — reported
// as: pick Missed, reload, and the row has moved back to All. The URL is the
// only place a selection can survive a reload, and the round trip below is the
// property that was missing.
describe('the chip a Home URL names', () => {
  it('is All when the address says nothing', () => {
    expect(chipFromSearch(new URLSearchParams(''), undefined)).toBe(ALL_CATEGORY)
  })

  it('survives the round trip for every chip', () => {
    for (const chip of [ALL_CATEGORY, 'Missed', 'Live', 'Science & Technology', 'Âm nhạc']) {
      const search = searchForChip(new URLSearchParams(''), chip)
      expect(chipFromSearch(search, undefined), chip).toBe(chip)
    }
  })

  it('keeps All out of the address rather than writing it down', () => {
    // A default spelled out is a URL that cannot be shortened and a history
    // entry that differs from the one a fresh visit produces.
    expect(searchForChip(new URLSearchParams(''), ALL_CATEGORY).toString()).toBe('')
  })

  it('leaves other parameters alone', () => {
    const before = new URLSearchParams('utm=x&chip=Live')
    const after = searchForChip(before, 'Missed')
    expect(after.get('utm')).toBe('x')
    expect(after.get('chip')).toBe('Missed')
    // And the input is not mutated, or a caller reading it again gets the
    // answer to a question it has not asked yet.
    expect(before.get('chip')).toBe('Live')
  })

  it('lets a topic route win over the address', () => {
    // /topic/Music is a real route the sidebar and the search box link to, and
    // it names the selection on its own. A stale `?chip=` must not fight it.
    expect(chipFromSearch(new URLSearchParams('chip=Live'), 'Music')).toBe('Music')
  })
})
