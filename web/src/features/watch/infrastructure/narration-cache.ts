/**
 * Translations, kept beside the video they belong to.
 *
 * A cue's translation is a pure function of its text and never changes, so
 * paying for it once per browser session — which is what an in-memory map
 * amounted to — was paying for the same sentence forever. The store lives on
 * the server so a reload, a second viewer, and the TV all read the same answer.
 *
 * Keyed by the hash of the cue text rather than by position, because the cue
 * grouping in narration-vtt.ts has been retuned a dozen times; a positional key
 * would throw the whole cache away on the next tweak, a content key loses only
 * the cues that actually changed.
 */

import { sha1Hex } from './sha1'

/**
 * Hex SHA-1 of a cue's text.
 *
 * Computed in JavaScript rather than through `crypto.subtle`, which exists only
 * in a secure context. This library is served over plain HTTP to a LAN address
 * (CLAUDE.md §2), where that API is undefined — and `localhost` is exempt from
 * the rule, so every test and every curl passed while the browser on the house
 * network threw on the first cue and took the whole translation pass down with
 * it, before it could send a single request to blame.
 *
 * Still async: the callers await it, and keeping the signature means the day
 * this page is served over HTTPS nothing has to change back.
 */
export async function hashCue(text: string): Promise<string> {
  return sha1Hex(text)
}

/**
 * Which partition of the on-disk file these translations belong in.
 *
 * The model is part of it. Two models translate the same line differently, and
 * a shared partition would blend them into one subtitle file with nothing to
 * say which line came from where — the same fault that made the engine name
 * part of the key when there were several engines.
 *
 * Keeping old partitions rather than clearing them makes trying a model a
 * reversible experiment: switch away and back, and the earlier work is still
 * there.
 */
let _partition = ''
let _partitionWaiters: Array<() => void> = []

export function setCachePartition(model: string) {
  _partition = model ? `omniroute:${model}` : 'omniroute'
  const waiters = _partitionWaiters
  _partitionWaiters = []
  waiters.forEach((w) => w())
}

/**
 * Resolves once the configured model is known.
 *
 * The pass has to wait for it. Starting before the answer arrives would mean
 * reading and writing a partition named after the wrong model — worse than a
 * cold cache, because the translations land somewhere they will be read back as
 * another model's work.
 */
export function whenPartitionReady(): Promise<void> {
  if (_partition) return Promise.resolve()
  return new Promise((resolve) => _partitionWaiters.push(resolve))
}

export async function loadNarrationCache(
  videoId: string,
): Promise<Map<string, string>> {
  try {
    const resp = await fetch(
      `/api/videos/${videoId}/narration-cache?engine=${_partition}`,
    )
    if (!resp.ok) return new Map()
    const body = (await resp.json()) as { entries?: Record<string, string> }
    return new Map(Object.entries(body.entries ?? {}))
  } catch {
    // The store is an optimisation. Losing it costs time, not correctness.
    return new Map()
  }
}

export async function saveNarrationCache(
  videoId: string,
  entries: Map<string, string>,
): Promise<void> {
  if (entries.size === 0) return
  try {
    await fetch(`/api/videos/${videoId}/narration-cache`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ engine: _partition, entries: Object.fromEntries(entries) }),
    })
  } catch {
    // Same reason as above.
  }
}

/**
 * Store the cue list exactly as it was grouped, beside the video.
 *
 * Written, never read back. The value is having the artifact on disk next to
 * the media and the translations — inspectable, and reusable by anything
 * server-side that needs the same cues. Reading it back would be a trap: the
 * grouping rules have been retuned a dozen times, and a client trusting a
 * stored copy would go on speaking last month's cues until someone deleted it.
 */
export async function saveNarrationCues(
  videoId: string,
  cues: Array<{ start: number; end: number; text: string }>,
): Promise<void> {
  if (!videoId || cues.length === 0) return
  try {
    await fetch(`/api/videos/${videoId}/narration-cues`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(cues),
    })
  } catch {
    // An artifact for humans and future jobs. Nothing here depends on it.
  }
}

/** Store the finished translation as a subtitle track beside the video. */
export async function saveNarrationVtt(
  videoId: string,
  vtt: string,
): Promise<boolean> {
  if (!videoId || !vtt) return false
  try {
    const resp = await fetch(`/api/videos/${videoId}/narration-vtt`, {
      method: 'POST',
      headers: { 'Content-Type': 'text/vtt' },
      body: vtt,
    })
    // Reported, because this file existing is what lets the gateway offer the
    // translation as a subtitle track — the caller has to know when to go and
    // ask for the list again.
    return resp.ok
  } catch {
    return false
  }
}

/**
 * Drop the machine translation for a video.
 *
 * For the case where the video's own Vietnamese track turns up while a pass is
 * running: the translation is then redundant, and leaving the file behind puts
 * a second Vietnamese entry in the caption menu for good.
 *
 * The cache is not touched. It cost real tokens, nothing reads it while a human
 * track exists, and throwing it away only means paying again.
 */
export async function deleteNarrationVtt(videoId: string): Promise<void> {
  if (!videoId) return
  try {
    await fetch(`/api/videos/${videoId}/narration-vtt`, { method: 'DELETE' })
  } catch {
    // Best effort. A file left on disk is untidy, not broken.
  }
}
