/**
 * Whether a thumbnail that loaded is really YouTube's "no thumbnail" tile.
 *
 * A missing thumbnail on i.ytimg.com answers 404 — and serves a valid 1097-byte
 * JPEG with it. The browser decodes that happily and fires `load`, not `error`,
 * so the usual fallback never runs and the card shows YouTube's grey tile
 * instead of ours. Measured against a video whose thumbnails are gone: every
 * variant, hqdefault through sddefault, returned 404 with the same 120x90 image.
 *
 * So the signature is its size. Guarded by the variant that was asked for,
 * because 120x90 is also the honest size of `default.jpg` — asking for that and
 * receiving it is not a failure, while asking for hqdefault and receiving a
 * 120x90 is.
 */
export function isMissingThumbnail(
  src: string,
  width: number,
  height: number,
): boolean {
  if (width !== 120 || height !== 90) return false
  return /\/(hq|mq|sd|maxres)default\.jpg/.test(src)
}
