/**
 * Turns a stored artwork reference into something an <img> can load.
 *
 * The catalogue stores two different kinds of reference in the same shape of
 * field. Channel artwork is downloaded and kept under the media root, so it
 * arrives as a path relative to it — "channels/UC…/avatar.jpg". Video
 * thumbnails are hotlinked and arrive as absolute URLs, because fetching a
 * couple of hundred images for videos that mostly will not be watched would
 * cost more disk than the videos actually kept.
 *
 * Handing a relative path straight to an <img> resolves it against the current
 * page, so a channel avatar on /watch/abc was requested from
 * /watch/channels/UC…/avatar.jpg and quietly 404ed. That is how avatars came to
 * be missing everywhere except the two places that happened to prefix it by
 * hand.
 */
export function mediaURL(reference: string | undefined): string | undefined {
  if (!reference) return undefined
  if (reference.startsWith('http://') || reference.startsWith('https://')) return reference
  if (reference.startsWith('/')) return reference
  return `/media/${reference}`
}
