/** How often to ask the catalogue about a video again, or false to stop. */
export const VIDEO_POLL_MS = 3000

/**
 * Polling stops once there is nothing left to wait for.
 *
 * It used to stop on mediaState alone, on the reasoning that FetchSubtitles is
 * fired at play and the media download is the slower of the two. That is a race,
 * not a rule, and a short video loses it: the file finishes first, the query
 * stops asking, and the subtitles published a second later are never seen. The
 * translation pass waits on cues that will not arrive until the page is
 * reloaded — which is why this only ever showed up on a brand-new video, and
 * only sometimes.
 *
 * So the condition is what the caller actually needs: media *and* subtitles.
 *
 * The attempt cap is what keeps that honest. Plenty of videos have no subtitles
 * at all, and for those "wait until subtitles arrive" is a request to poll for
 * as long as the page is open — each one an EnsureVideo, which is a full
 * metadata fetch at the far end (CLAUDE.md §8, risk 5). Two minutes is well past
 * how long fetching a subtitle track takes and well short of a habit.
 */
export const MAX_POLLS = 40

export function videoPollInterval(
  video: { mediaState?: string; subtitles?: unknown[] } | undefined,
  attempts: number,
): number | false {
  if (attempts >= MAX_POLLS) return false
  // Nothing back yet: the query is still in flight, and its own retry handles it.
  if (!video) return VIDEO_POLL_MS
  const settled = video.mediaState === 'READY' || video.mediaState === 'FAILED'
  const hasSubtitles = (video.subtitles?.length ?? 0) > 0
  if (settled && hasSubtitles) return false
  return VIDEO_POLL_MS
}
