/** How often to ask the catalogue about a video again, or false to stop. */
export const VIDEO_POLL_MS = 3000

/**
 * Polling stops once the subtitles are in, or once the cap is reached.
 *
 * It used to stop on mediaState alone, on the reasoning that FetchSubtitles is
 * fired at play and the media download is the slower of the two. That is a race,
 * not a rule, and a short video loses it: the file finishes first, the query
 * stops asking, and the subtitles published a second later are never seen.
 *
 * **The media half of that condition is gone, and it was worse than redundant.**
 * `READY` is not reachable at all when caching is switched off (CLAUDE.md §4,
 * "Streaming only") — nothing is ever downloaded, so a row left at `DOWNLOADING`
 * stays there for good. Measured on two videos of this library, both added days
 * earlier and both still `DOWNLOADING` with their subtitles already on disk. So
 * the condition could never be satisfied: every watch page ran the full forty
 * polls, each one an EnsureVideo — a full metadata fetch at the far end, which
 * is the exact thing §8 risk 6 counts — and then stopped, after which subtitles
 * arriving late were only ever seen by reloading the page.
 *
 * Nothing is lost by dropping it. Whether the file has landed is what
 * `useStream` polls for, and that poll has its own stopping rule; this query
 * exists for the subtitle list.
 *
 * The attempt cap is what keeps waiting-for-subtitles honest. Plenty of videos
 * have none at all, and for those "wait until subtitles arrive" is a request to
 * poll for as long as the page is open. Two minutes is well past how long
 * fetching a subtitle track takes and well short of a habit.
 */
export const MAX_POLLS = 40

export function videoPollInterval(
  video: { mediaState?: string; subtitles?: unknown[] } | undefined,
  attempts: number,
): number | false {
  if (attempts >= MAX_POLLS) return false
  // Nothing back yet: the query is still in flight, and its own retry handles it.
  if (!video) return VIDEO_POLL_MS
  if ((video.subtitles?.length ?? 0) > 0) return false
  return VIDEO_POLL_MS
}
