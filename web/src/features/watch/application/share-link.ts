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

/**
 * `cancelled` and `failed` are separate because they are said differently: one
 * is the viewer's own answer and deserves no report at all, the other is the
 * button not working and must never be silent.
 */
export type ShareOutcome = 'shared' | 'copied' | 'cancelled' | 'failed'

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
      // Cancelling the sheet rejects, and cancelling is not a failure — nor is
      // it a reason to put something on the clipboard, which would be doing the
      // thing that was just declined. Nothing happens and nothing is said.
      return 'cancelled'
    }
  }

  return (await copyText(url)) ? 'copied' : 'failed'
}

/**
 * Put text on the clipboard, including where the modern API is withheld.
 *
 * This app is served over plain HTTP on a LAN address (CLAUDE.md §8, risk 3:
 * HTTPS is still unproven), and **a plain-HTTP origin is not a secure context**,
 * so browsers withhold `navigator.clipboard` entirely — the property is not
 * merely permission-gated, it is absent. Only `localhost` is exempt, which is
 * exactly why this worked on the development machine and did nothing on a phone.
 *
 * `document.execCommand('copy')` is deprecated and carries none of that gating.
 * It is the only clipboard a page on this network has, so it is not a fallback
 * for an exotic browser — on every device but the one running the dev server it
 * is *the* path.
 */
async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Present but refused — permission, or a document that is not focused.
      // Fall through: the old road may still be open.
    }
  }

  const field = document.createElement('textarea')
  field.value = text
  // Off-screen rather than hidden: the selection has to be real, and neither
  // `display:none` nor `hidden` can be selected. `readOnly` keeps the keyboard
  // down on a phone in the moment the field holds focus.
  field.setAttribute('readonly', '')
  field.style.position = 'fixed'
  field.style.top = '-1000px'
  field.style.opacity = '0'
  document.body.appendChild(field)
  try {
    field.select()
    field.setSelectionRange(0, text.length) // iOS ignores select() alone.
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    field.remove()
  }
}
