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

export type NarrationEngine = 'nllb' | 'qwen'

/** Hex SHA-1 of a cue's text. */
export async function hashCue(text: string): Promise<string> {
  const bytes = new TextEncoder().encode(text)
  const digest = await crypto.subtle.digest('SHA-1', bytes)
  return Array.from(new Uint8Array(digest))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}

export async function loadNarrationCache(
  videoId: string,
  engine: NarrationEngine,
): Promise<Map<string, string>> {
  try {
    const resp = await fetch(
      `/api/videos/${videoId}/narration-cache?engine=${engine}`,
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
  engine: NarrationEngine,
  entries: Map<string, string>,
): Promise<void> {
  if (entries.size === 0) return
  try {
    await fetch(`/api/videos/${videoId}/narration-cache`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ engine, entries: Object.fromEntries(entries) }),
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
): Promise<void> {
  if (!videoId || !vtt) return
  try {
    await fetch(`/api/videos/${videoId}/narration-vtt`, {
      method: 'POST',
      headers: { 'Content-Type': 'text/vtt' },
      body: vtt,
    })
  } catch {
    // The player reads its translations from memory. This file is for
    // everything that is not the player.
  }
}
