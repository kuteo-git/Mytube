import type { StreamSources } from '@/features/catalog/infrastructure/catalogRepository'
import type { QualityChoice } from '@/features/watch/application/autoplay'
import { shouldUseHLS } from '@/features/watch/application/hls-source'

/**
 * Which source the player should be on, decided without React and without an
 * element.
 *
 * ## Why this is its own file
 *
 * These three decisions — what is available, what to open on, what to move to —
 * lived inside a 4000-line component among 136 hooks, and were reachable only
 * by rendering a whole watch page with a mocked repository. They are pure
 * functions of a server answer and a preference, and every question worth
 * asking about them can be asked in a line.
 *
 * ## Every tier here can seek
 *
 * That is the point of the file, and of the change that produced it. The muxed
 * stream could not: it is fMP4 down a pipe with no index, so "seek" meant
 * reopening the stream at a mark, which meant knowing where the mark really
 * landed, which meant a keyframe probe, an offset carried through every
 * position calculation, a lead measured from a moving playhead, a handover
 * timed to the second, and a reopen budget for when it arrived late. Roughly a
 * thousand lines, all of it in service of one missing index.
 *
 * HLS has an index — a media playlist *is* one — so the browser seeks by
 * itself, and all of that goes. What is left is: pick the best source, and
 * change over cleanly when a better one appears.
 *
 * ## The muxed tier is not offered
 *
 * Every browser this is built for (CLAUDE.md §2 — phone browsers and desktop)
 * plays HLS: natively on Safari and iOS, through hls.js anywhere with
 * MediaSource. And measured 2026-08-20 on iOS 18.7, the mux never worked there
 * at all — it reached `play()` and produced no picture.
 *
 * A browser with neither is given no upstream tier and waits for the download.
 * That is not a new behaviour: §4 already answers "no tier and no error" when
 * upstream has nothing playable, and the player has a progress bar for exactly
 * that. Offering a stream that cannot be seeked would bring the whole apparatus
 * above back for a browser nobody here owns.
 */

export type TierName = 'hls' | 'local'

export interface Tier {
  name: TierName
  url: string
  height?: number
}

/**
 * The rendition the muxed tier is served at unless asked otherwise, and the one
 * HLS is built from. A copy of ingest's `LIVE_HEIGHT`.
 */
export const DEFAULT_LIVE_HEIGHT = 720

/** The rendition a viewer gets by asking for the high one. */
export const PINNED_HEIGHT = 1080

/**
 * Everything that can play right now, best first.
 *
 * "Best" is not a judgement about picture: `local` is first because it is on
 * the disk, needs nothing from upstream and cannot be refused mid-video.
 */
export function availableTiers(
  sources: StreamSources | undefined,
  choice: QualityChoice,
): Tier[] {
  if (!sources) return []
  const tiers: Tier[] = []

  if (sources.local) {
    tiers.push({ name: 'local', url: sources.local.url })
  }

  // Offered only where the browser can actually play it. `shouldUseHLS` reads
  // the engine rather than `canPlayType`, which answers "maybe" both on the
  // browser that plays a playlist and on the one that refuses it.
  if (sources.hls && shouldUseHLS()) {
    tiers.push({
      name: 'hls',
      url: sources.hls.url,
      // Pinning the high rendition is an order. Auto keeps the server's own
      // height: this tier only has to last until the copy lands — a median of
      // thirteen seconds — and twice the bytes is twice the wait.
      height: choice === 'high' ? PINNED_HEIGHT : (sources.hls.height ?? DEFAULT_LIVE_HEIGHT),
    })
  }

  return tiers
}

/**
 * What to open on.
 *
 * The file on disk whenever there is one, because it is strictly better and
 * costs nothing. Otherwise whatever else is available.
 *
 * There is no longer a "fastest to start" tier separate from the best one — the
 * progressive rendition that used to fill that role stopped serving (§4), and
 * the tier that replaced it is the same one the player would climb to anyway.
 */
export function openingTier(tiers: Tier[]): Tier | undefined {
  return tiers.find((t) => t.name === 'local') ?? tiers[0]
}

/**
 * The tier to move to, or undefined when the player is already on the best one.
 *
 * The only move left is the local file arriving mid-watch. Everything else the
 * old ladder did — climbing off a low rendition, retreating from a mux that
 * broke, reopening one that arrived late — belonged to tiers that no longer
 * exist.
 */
export function targetTier(
  tiers: Tier[],
  current: TierName | undefined,
  localFailed: boolean,
): Tier | undefined {
  const local = tiers.find((t) => t.name === 'local')
  // A file that will not load is not worth asking for again: the disk is not
  // going to change its mind between two polls of the same answer.
  const wanted = localFailed ? tiers.find((t) => t.name !== 'local') : (local ?? tiers[0])
  if (!wanted || wanted.name === current) return undefined
  return wanted
}

/**
 * What to call the tier on screen.
 *
 * The height, because that is the question a viewer is asking. "Live" marks the
 * one being streamed from upstream rather than read from the disk — the picture
 * is the same, but it can stop if the network does, and that is worth knowing.
 */
export function tierLabel(tier: Tier | undefined, localHeight?: number): string {
  if (!tier) return ''
  if (tier.name === 'local') return `${localHeight ?? PINNED_HEIGHT}p`
  return `${tier.height ?? DEFAULT_LIVE_HEIGHT}p live`
}
