/**
 * How hard to try again when a batch comes back with nothing.
 *
 * A failed batch used to be lost for the rest of the pass: the loop moved on
 * and those cues were never asked for again, so one blip left a permanent hole
 * that only changing a setting — which restarts the pass — could fill.
 *
 * But trying forever is its own fault. CLAUDE.md §8 records what happened the
 * last time this project pushed against a wall: a backfill kept firing at a
 * rate limit and turned a temporary block into a longer one. It stops after
 * fifteen consecutive failures now, for that reason. The same reasoning, at a
 * smaller scale, is here.
 */

/** Tries per batch, the first one included. */
export const BATCH_ATTEMPTS = 3

/**
 * Consecutive failed batches before the pass stops.
 *
 * Three batches failing back to back is not a blip, it is the translator being
 * down — and a video's worth of retries against something that is down is a
 * long argument nobody wins.
 */
export const GIVE_UP_AFTER = 3

/**
 * Wait before attempt n, counting from zero.
 *
 * The first retry is quick because most failures are momentary; the second
 * waits long enough to be worth calling a retry rather than a repeat.
 */
export function retryDelayMs(attempt: number): number {
  if (attempt <= 0) return 0
  return attempt === 1 ? 1_000 : 3_000
}

/**
 * Whether a batch that produced nothing is worth asking for again.
 *
 * An abort is not: the viewer left the video, and the request was cancelled on
 * purpose. Retrying it would be work for a page nobody is on.
 *
 * A model that answered but returned nothing usable is not either — it did
 * reply, so the trip worked, and the same prompt will get the same answer.
 */
export function worthRetrying({
  aborted,
  error,
}: {
  aborted: boolean
  /** What translateBatch recorded, or '' when it did not fail. */
  error: string
}): boolean {
  if (aborted) return false
  if (error === '') return false
  return !error.includes('all empty')
}
