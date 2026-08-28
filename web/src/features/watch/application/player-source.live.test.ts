import { describe, expect, it } from 'vitest'

import { availableTiers, openingTier, targetTier } from './player-source'
import type { StreamSources } from '@/features/catalog/infrastructure/catalogRepository'

/**
 * A broadcast still on air stands alone.
 *
 * Everything else in this ladder exists because a file is on its way: the
 * player opens on the best tier upstream can serve and climbs to the local copy
 * when it lands, a median of thirteen seconds later. For a live video no copy
 * is ever coming — §4 refuses a broadcast as a download job, because it has no
 * end to download to — so the climb has nothing to climb to and every mechanism
 * that measures a lead, parks at a mark or hands over between layers is
 * machinery running against a stationary target.
 */
const liveOnly: StreamSources = {
  live: { url: '/api/live/abc/master.m3u8', seekable: true },
} as StreamSources

describe('the live tier', () => {
  it('is the only tier offered, even when others are somehow present', () => {
    // The guard has to hold against a server that answers with both. It should
    // not, but "should not" is not a thing the player can rely on, and the cost
    // of being wrong is the climb machinery starting on a video with no end.
    const contradictory = {
      ...liveOnly,
      local: { url: '/media/abc/1080p.mp4', seekable: true },
      hls: { url: '/api/videos/abc/hls', seekable: true },
    } as StreamSources

    const tiers = availableTiers(contradictory, 'auto')

    expect(tiers.map((t) => t.name)).toEqual(['live'])
  })

  it('is what the player opens on', () => {
    expect(openingTier(availableTiers(liveOnly, 'auto'))?.name).toBe('live')
  })

  it('schedules no climb, because there is nothing to climb to', () => {
    const tiers = availableTiers(liveOnly, 'auto')

    expect(targetTier(tiers, 'live', false)).toBeUndefined()
  })

  it('is not pushed to 1080p by pinning the high rendition', () => {
    // The ladder is YouTube's own, read from the playlist by the player. There
    // is no height to ask the server for here, and inventing one would put a
    // number in a URL that means nothing on this route.
    const tiers = availableTiers(liveOnly, 1080)

    expect(tiers).toHaveLength(1)
    expect(tiers[0].height).toBeUndefined()
  })
})
