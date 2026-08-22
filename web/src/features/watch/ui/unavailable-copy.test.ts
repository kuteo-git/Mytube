import { describe, expect, it } from 'vitest'
import { unavailableCopy } from './Player'
import i18n from '@/shared/i18n'

// The real translator, in the language the suite is pinned to. A stub would
// make these tests assert that a mock was called rather than that the copy
// says what it should.
const t = i18n.t.bind(i18n)

/**
 * A video YouTube will not hand over used to reach the viewer as a 500 and a
 * blank picture. Each reason leads somewhere different, so each gets its own
 * sentence: a members-only video can be unlocked by joining the channel, a
 * removed one is gone for everybody.
 */
describe('the sentence for a video that cannot be fetched', () => {
  it('names the membership, because that is the way in', () => {
    expect(unavailableCopy('members_only', t)).toMatch(/members-only/i)
    expect(unavailableCopy('members_only', t)).toMatch(/join the channel/i)
  })

  it('says private for a private video', () => {
    expect(unavailableCopy('private', t)).toMatch(/private/i)
  })

  it('says removed for one that is gone', () => {
    expect(unavailableCopy('removed', t)).toMatch(/removed/i)
  })

  // The catch-all still has to be a sentence a person can act on, not "error".
  it('falls back to something plain rather than nothing', () => {
    expect(unavailableCopy('unavailable', t)).toMatch(/cannot be fetched/i)
  })

  // None of them may offer a retry: there is nothing to try again.
  it('never suggests trying again', () => {
    for (const reason of ['members_only', 'private', 'removed', 'unavailable'] as const) {
      expect(unavailableCopy(reason, t)).not.toMatch(/try again|retry|re-download/i)
    }
  })
})
