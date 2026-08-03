import type { NarrationEngine } from '@/features/watch/infrastructure/narration-cache'

/**
 * Cues in the opening batch.
 *
 * Small on purpose. A full batch of fifteen was measured at about twenty
 * seconds, and twenty seconds of silence after switching narration on reads as
 * broken. Three cues is enough to start talking; by the time they are spoken
 * the background pass is already well ahead, because translation outruns speech
 * by between two and four times.
 */
export const FIRST_BATCH = 3

/** Cues per batch once playback is under way. */
export const BATCH_SIZE = 15

/** Preceding cues sent along for context, not for translation. */
export const CONTEXT_CUES = 3

export function planBatches(
  total: number,
): Array<{ start: number; end: number }> {
  const out: Array<{ start: number; end: number }> = []
  let i = 0
  while (i < total) {
    const size = out.length === 0 ? FIRST_BATCH : BATCH_SIZE
    out.push({ start: i, end: Math.min(i + size, total) })
    i += size
  }
  return out
}

export async function translateBatch(
  cues: string[],
  context: string[],
  engine: NarrationEngine,
): Promise<string[]> {
  const blank = cues.map(() => '')
  if (cues.length === 0) return []
  try {
    const resp = await fetch('/api/translate/batch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ engine, cues, context }),
    })
    if (!resp.ok) return blank
    const body = (await resp.json()) as { translations?: string[] }
    const got = body.translations ?? []
    // Length is load-bearing: the caller maps these onto cues by position, so a
    // short answer must be padded rather than allowed to shift everything after
    // it onto the wrong cue.
    return cues.map((_, i) => got[i] ?? '')
  } catch {
    return blank
  }
}
