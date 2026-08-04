/**
 * How the home feed's new material is divided, in whole percent.
 *
 * Three numbers rather than the ranker's nine reasons. The reasons overlap —
 * a video can be unwatched *and* from a subscribed channel, and only one of
 * those decides which share it takes — so offering them as controls would be
 * offering a vocabulary rather than a feed. These three are mutually exclusive
 * by construction: subscribed, or not subscribed and matching this viewer's
 * taste, or neither.
 */
export interface FeedMix {
  subscribedPercent: number
  affinityPercent: number
  discoveryPercent: number
}

export type FeedMixKey = keyof FeedMix

export const FEED_MIX_KEYS: FeedMixKey[] = [
  'subscribedPercent',
  'affinityPercent',
  'discoveryPercent',
]

/**
 * The two shares nobody can move, and why they are not on the page.
 *
 * Finishing something you started and going back to something you finished are
 * states of the watch history, not sources of new material. Making them compete
 * with the sliders would mean a viewer who wants more discovery is asked to
 * give up the video they were halfway through.
 *
 * Kept here so the note under the sliders and the arithmetic in the readout
 * cannot drift apart. Mirrors shareContinueWatching/shareRewatch in the ranker.
 */
export const FIXED_SHARES = { continueWatching: 10, rewatch: 8 } as const

/** What the three sliders divide between them. */
export const ADJUSTABLE_PERCENT =
  100 - FIXED_SHARES.continueWatching - FIXED_SHARES.rewatch

/** The window the ratios are applied over, matching the ranker's page size. */
export const FEED_WINDOW = 24

export function mixTotal(mix: FeedMix): number {
  return mix.subscribedPercent + mix.affinityPercent + mix.discoveryPercent
}

/**
 * Move one slider and let the other two absorb the difference.
 *
 * The three always add to a hundred, so something has to give. The remainder is
 * split in proportion to where the other two already were, which is the only
 * division that leaves their relationship to each other untouched — dragging
 * "subscribed" up should not quietly decide that you now prefer discovery to
 * affinity.
 *
 * When the other two are both at zero there is no ratio to preserve, so they
 * split what is left evenly. Anything else would have to invent a preference.
 */
export function setShare(mix: FeedMix, key: FeedMixKey, value: number): FeedMix {
  const target = clampPercent(value)
  const others = FEED_MIX_KEYS.filter((k) => k !== key)
  const remainder = 100 - target
  const othersTotal = others.reduce((sum, k) => sum + mix[k], 0)

  const next = { ...mix, [key]: target } as FeedMix
  if (othersTotal <= 0) {
    const half = Math.round(remainder / 2)
    next[others[0]] = half
    next[others[1]] = remainder - half
    return next
  }

  // The first is rounded and the second takes what is left, so the three always
  // add to exactly a hundred however the rounding falls.
  const first = Math.round((mix[others[0]] / othersTotal) * remainder)
  next[others[0]] = first
  next[others[1]] = remainder - first
  return next
}

function clampPercent(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(100, Math.max(0, Math.round(value)))
}

/**
 * How many of the next twenty-four videos a share works out as.
 *
 * A percentage of a percentage of a page is two pieces of arithmetic nobody
 * should have to do in their head to find out that thirty per cent means seven
 * videos. Shown beside each slider for the same reason the storage page shows
 * gigabytes rather than a ratio.
 */
export function videosPerWindow(percent: number): number {
  return Math.round((percent / 100) * (ADJUSTABLE_PERCENT / 100) * FEED_WINDOW)
}
