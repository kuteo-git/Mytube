/**
 * Sharing a video means sharing the video, not this library.
 *
 * The link used to be `window.location.href` — an address on the house LAN,
 * which is only a link at all to somebody sitting in the house and on the same
 * network. Sent anywhere else it is a dead string. The YouTube address is the
 * one that means the same thing to everybody, and it is the address this
 * library got the video from in the first place.
 *
 * That reverses CLAUDE.md §5, which recorded Share as copying the LAN link.
 */

/** The public address of a video. */
export function shareURL(video: { id: string; sourceUrl?: string }): string {
  // What ingest recorded when it fetched the video, where there is one: it is
  // the real address rather than one assembled from a guess about the id.
  const source = video.sourceUrl?.trim()
  if (source && /^https?:\/\//.test(source)) return source
  return `https://www.youtube.com/watch?v=${video.id}`
}

export type ShareOutcome = 'shared' | 'copied' | 'failed'

/**
 * Hand the link to the device, or to the clipboard.
 *
 * A phone has a share sheet and expects a share button to open it — copying
 * silently there is a button that appears to do nothing. A desktop mostly has
 * no sheet, and a link on the clipboard is what people do next anyway.
 *
 * `canShare` is asked of the caller rather than worked out here, because the
 * answer is two things — whether the browser has the API at all, and whether
 * this is a device where a sheet is the right answer — and the second is a
 * question about the pointer that this module has no business asking.
 */
export async function shareVideo({
  url,
  title,
  canShare,
}: {
  url: string
  title?: string
  canShare: boolean
}): Promise<ShareOutcome> {
  if (canShare && typeof navigator !== 'undefined' && navigator.share) {
    try {
      await navigator.share({ url, title })
      return 'shared'
    } catch {
      // Cancelling the sheet rejects, and cancelling is not a failure — but it
      // is not a reason to put something on the clipboard either. Either way
      // there is nothing to report, so nothing is said.
      return 'failed'
    }
  }

  try {
    await navigator.clipboard.writeText(url)
    return 'copied'
  } catch {
    // No clipboard permission, or an insecure context — which this app is, on a
    // plain-HTTP LAN address, in every browser but Chrome's localhost
    // exemption. Worth reporting rather than pretending: see CLAUDE.md §2.
    return 'failed'
  }
}
