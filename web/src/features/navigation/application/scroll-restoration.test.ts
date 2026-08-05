import { describe, expect, it } from 'vitest'
import {
  type ScrollDecision,
  canReach,
  isTabRoot,
  scrollTargetFor,
} from './scroll-restoration'

const decide = (over: Partial<ScrollDecision>) =>
  scrollTargetFor({
    kind: 'PUSH',
    tabRoot: false,
    samePath: false,
    savedForEntry: undefined,
    savedForPath: undefined,
    ...over,
  })

describe('switching tabs', () => {
  it('returns a tab to where you left it', () => {
    // The behaviour a phone taught everyone, and the one a plain "scroll to top
    // on navigate" gets wrong: Home scrolled halfway, over to History, back to
    // Home — still halfway.
    expect(decide({ tabRoot: true, savedForPath: 1200 })).toBe(1200)
  })

  it('opens a tab never visited at the top', () => {
    expect(decide({ tabRoot: true, savedForPath: undefined })).toBe(0)
  })

  it('ignores the entry memory when arriving at a tab', () => {
    // A tab returned to is a *new* history entry, so it has no memory of its
    // own; the position belongs to the path. Reading the entry here would
    // always find nothing and always land at the top.
    expect(decide({ tabRoot: true, savedForEntry: 50, savedForPath: 1200 })).toBe(
      1200,
    )
  })

  it('takes you to the top when you tap the tab you are already on', () => {
    // Every phone does this, and in a feed that never ends it is the only quick
    // way back to the start.
    //
    // It arrives as a REPLACE, not a PUSH: React Router collapses a link to the
    // location you are already on. Measured, and the reason the same-path check
    // has to sit above the REPLACE rule rather than below it — tested here in
    // both spellings so a reordering cannot pass.
    expect(decide({ tabRoot: true, samePath: true, savedForPath: 1200 })).toBe(0)
    expect(
      decide({ kind: 'REPLACE', tabRoot: true, samePath: true, savedForPath: 1200 }),
    ).toBe(0)
  })
})

describe('drilling in', () => {
  it('opens a screen you have not seen at the top', () => {
    // The fault this all exists for: a single-page router never reloads the
    // document, so opening a video from halfway down Home began halfway down
    // the video page — which on a short page is past the end of it, and reads
    // as a blank screen rather than as a scroll position.
    expect(decide({ tabRoot: false })).toBe(0)
  })

  it('opens it at the top even if that path was visited before', () => {
    // A video is somewhere you arrive, not somewhere you return to. Landing
    // partway down it because you once scrolled its comments would be a
    // surprise every time.
    expect(decide({ tabRoot: false, savedForPath: 900 })).toBe(0)
  })
})

describe('going back', () => {
  it('returns to the exact entry you came from', () => {
    expect(decide({ kind: 'POP', savedForEntry: 1200 })).toBe(1200)
  })

  it('prefers the entry over the path', () => {
    // Two visits to the same feed are two entries with two positions. Stepping
    // back through both should return to each in turn, so the path memory —
    // which only ever holds the most recent — must not win here.
    expect(
      decide({ kind: 'POP', savedForEntry: 300, savedForPath: 1200 }),
    ).toBe(300)
  })

  it('goes to the top on a back with nothing recorded', () => {
    // An entry from before this session — the top is the only honest answer.
    expect(decide({ kind: 'POP' })).toBe(0)
  })

  it('is not confused by landing on the path it started from', () => {
    // Back from /watch to / is a POP that changes path; back within one path
    // still has an entry to honour, and samePath must not steal it.
    expect(
      decide({ kind: 'POP', samePath: true, savedForEntry: 700 }),
    ).toBe(700)
  })
})

describe('an address merely restated', () => {
  it('leaves the page exactly where it is', () => {
    // REPLACE is not a journey. In this app it is a search query being edited
    // and a redirect away from an unknown path; scrolling would yank the page
    // out from under someone who only typed a character.
    expect(decide({ kind: 'REPLACE' })).toBeNull()
    expect(decide({ kind: 'REPLACE', tabRoot: true, savedForPath: 900 })).toBeNull()
    // A search query being edited: same path, but not a tab you return to.
    expect(
      decide({ kind: 'REPLACE', samePath: true, tabRoot: false }),
    ).toBeNull()
  })
})

describe('which paths behave like tabs', () => {
  it('counts every navigation destination', () => {
    // The five in the bottom bar and the six in the sidebar. A path missing
    // here loses its position on every tab switch, silently.
    for (const p of [
      '/',
      '/subscriptions',
      '/saved',
      '/history',
      '/activity',
      '/settings',
      '/storage',
    ]) {
      expect(isTabRoot(p)).toBe(true)
    }
  })

  it('counts topic pages', () => {
    // Picking a chip is browsing the same feed through a filter, not opening
    // something new.
    expect(isTabRoot('/topic/Music')).toBe(true)
    expect(isTabRoot('/topic/Science & Technology')).toBe(true)
  })

  it('does not count the pages you drill into', () => {
    expect(isTabRoot('/watch/abc123')).toBe(false)
    // A channel is opened *from* Subscriptions, so going back to the list has
    // to return to where it was scrolled — which is the POP branch, keyed on
    // the entry. Counting it as a tab would send the channel itself back to
    // wherever a different channel had been left.
    expect(isTabRoot('/channel/UC123')).toBe(false)
    // Search is a question you ask afresh; returning to old results partway
    // down would be answering a different one.
    expect(isTabRoot('/results')).toBe(false)
  })
})

describe('waiting for the page to be tall enough', () => {
  it('accepts an offset the page can actually reach', () => {
    expect(canReach(1000, 3000, 800)).toBe(true)
  })

  it('accepts an offset exactly at the bottom', () => {
    expect(canReach(2200, 3000, 800)).toBe(true)
  })

  it('refuses while the page is still shorter than the offset', () => {
    // Returning to a feed means the grid is still being rebuilt. Landing now
    // would put the viewer at the bottom of a half-built page, which looks
    // exactly like the position having been forgotten.
    expect(canReach(2000, 1200, 800)).toBe(false)
  })

  it('always accepts the top', () => {
    // Otherwise a short page could never be restored to zero, and the retry
    // loop would spin for its full budget on every plain navigation.
    expect(canReach(0, 800, 800)).toBe(true)
  })
})
