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

export async function translateBatch(
  cues: string[],
  context: string[],
): Promise<string[]> {
  const blank = cues.map(() => '')
  if (cues.length === 0) return []
  try {
    const resp = await fetch('/api/translate/batch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ cues, context }),
    })
    if (!resp.ok) {
      _lastError = `máy chủ trả ${resp.status}`
      return blank
    }
    const body = (await resp.json()) as { translations?: string[] }
    const got = body.translations ?? []
    const filled = cues.map((_, i) => got[i] ?? '')
    if (filled.every((t) => !t)) {
      _lastError = `nhận ${got.length}/${cues.length} dòng, tất cả rỗng`
      return blank
    }
    _lastError = ''
    // Length is load-bearing: the caller maps these onto cues by position, so a
    // short answer must be padded rather than allowed to shift everything after
    // it onto the wrong cue.
    return filled
  } catch (e) {
    _lastError = e instanceof Error ? e.message : 'không gọi được'
    return blank
  }
}
