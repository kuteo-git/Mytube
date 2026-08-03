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

/**
 * Why the last batch produced nothing, or '' if it did not fail.
 *
 * Kept because the first version swallowed the error entirely, and a pass whose
 * every translation came back empty could not be told apart from a pass that
 * never ran. Diagnosing it cost two rounds of guessing at logs.
 */
let _lastError = ''

export function lastBatchError(): string {
  return _lastError
}

/**
 * The order cues are translated in: from the playhead to the end, then back
 * round to cover the beginning.
 *
 * Translating only from the playhead onwards left the earlier part of the video
 * untranslated for good — a backward seek fell silent even though the answers
 * were on disk, and the subtitle file written at the end began wherever the
 * viewer happened to have started. Wrapping costs nothing and owes nobody
 * anything: the next line the viewer needs is still first in the queue.
 */
export function workOrder(total: number, first: number): number[] {
  const out: number[] = []
  for (let i = 0; i < total; i++) out.push((first + i) % total)
  return out
}

export async function translateBatch(
  cues: string[],
  context: string[],
  signal?: AbortSignal,
): Promise<string[]> {
  const blank = cues.map(() => '')
  if (cues.length === 0) return []
  try {
    const resp = await fetch('/api/translate/batch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ cues, context }),
      signal,
    })
    if (!resp.ok) {
      _lastError = `server returned ${resp.status}`
      return blank
    }
    const body = (await resp.json()) as { translations?: string[] }
    const got = body.translations ?? []
    const filled = cues.map((_, i) => got[i] ?? '')
    if (filled.every((t) => !t)) {
      _lastError = `got ${got.length}/${cues.length} lines, all empty`
      return blank
    }
    _lastError = ''
    // Length is load-bearing: the caller maps these onto cues by position, so a
    // short answer must be padded rather than allowed to shift everything after
    // it onto the wrong cue.
    return filled
  } catch (e) {
    // Leaving the video is not a failure — and it clears whatever was there,
    // because this module's error outlives the pass that set it. Left alone, a
    // stale message would surface on the status line of the next video.
    _lastError = signal?.aborted
      ? ''
      : e instanceof Error
        ? e.message
        : 'request failed'
    return blank
  }
}
