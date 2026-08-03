/**
 * Force every cue to the middle, whatever the file says.
 *
 * A cue carries its own placement — `align:start position:0%` on all 463 cues
 * of a measured YouTube auto-caption file — and that placement beats any
 * stylesheet. ::cue can set a cue's colour, font and background; the properties
 * that decide where it sits are not among the ones CSS is allowed to touch.
 *
 * The gateway strips those settings when it serves a subtitle, but that is not
 * enough on its own: in the LAN deployment Caddy takes the /media/ route over
 * from the gateway, and would serve the file exactly as it is on disk. Doing it
 * here as well means the two subtitle sources agree no matter who served them.
 *
 * Written against the shape rather than TextTrackCue so it can be tested
 * without a browser.
 */
export interface PlaceableCue {
  align?: string
  line?: number | 'auto'
  position?: number | 'auto'
  size?: number
}

/**
 * Reset one cue to the default placement: centred, on the browser's own line,
 * across the full width.
 *
 * Assignments are guarded individually. Browsers disagree about which of these
 * are settable and an older one throwing on `position` should not stop `align`
 * from being applied.
 */
export function centreCue(cue: PlaceableCue): void {
  const defaults: PlaceableCue = {
    align: 'center',
    line: 'auto',
    position: 'auto',
    size: 100,
  }
  for (const key of Object.keys(defaults) as Array<keyof PlaceableCue>) {
    try {
      // @ts-expect-error — writing a union member back onto the same key.
      cue[key] = defaults[key]
    } catch {
      // Some engines reject some of these. Whatever lands is an improvement.
    }
  }
}

/**
 * Centre every cue a track has loaded. Safe to call repeatedly.
 *
 * Takes a loose type because the DOM's TextTrackCue declares none of these
 * properties — they belong to VTTCue, which is what a subtitle track actually
 * produces, and narrowing at the call site would be asserting something this
 * function is already careful not to assume.
 */
export function centreCues(cues: ArrayLike<object> | null): number {
  if (!cues) return 0
  for (let i = 0; i < cues.length; i++) centreCue(cues[i] as PlaceableCue)
  return cues.length
}
