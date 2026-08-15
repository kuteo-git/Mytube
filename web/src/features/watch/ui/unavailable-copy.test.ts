import { describe, expect, it } from 'vitest'
import { unavailableCopy } from './Player'

/**
 * A video YouTube will not hand over used to reach the viewer as a 500 and a
 * blank picture. Each reason leads somewhere different, so each gets its own
 * sentence: a members-only video can be unlocked by joining the channel, a
 * removed one is gone for everybody.
 */
describe('the sentence for a video that cannot be fetched', () => {
  it('names the membership, because that is the way in', () => {
    expect(unavailableCopy('members_only')).toMatch(/members-only/i)
    expect(unavailableCopy('members_only')).toMatch(/join the channel/i)
  })

  it('says private for a private video', () => {
    expect(unavailableCopy('private')).toMatch(/private/i)
  })

  it('says removed for one that is gone', () => {
    expect(unavailableCopy('removed')).toMatch(/removed/i)
  })

  // The catch-all still has to be a sentence a person can act on, not "error".
  it('falls back to something plain rather than nothing', () => {
    expect(unavailableCopy('unavailable')).toMatch(/cannot be fetched/i)
  })

  // None of them may offer a retry: there is nothing to try again.
  it('never suggests trying again', () => {
    for (const reason of ['members_only', 'private', 'removed', 'unavailable'] as const) {
      expect(unavailableCopy(reason)).not.toMatch(/try again|retry|re-download/i)
    }
  })
})
