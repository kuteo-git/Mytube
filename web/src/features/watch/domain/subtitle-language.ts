/**
 * The tag the gateway offers our own translation under. The "-x-" is BCP-47 for
 * a private extension, which is exactly what it is: not a language YouTube ever
 * shipped, but a file this app wrote.
 */
export const MACHINE_LANGUAGE = 'vi-x-mt'

/**
 * Whether the video came with Vietnamese of its own.
 *
 * Our own translation is deliberately not counted. It used to be — the test was
 * "any language starting vi" — and the effect was that the translator switched
 * itself off the moment it produced anything: the first batch wrote the file,
 * the subtitle list picked the track up, the video now looked as though it had
 * Vietnamese all along, and the whole translation group disappeared along with
 * the progress it was reporting. The pass carried on and the voice kept
 * speaking, with nothing on screen to say so.
 */
export function hasHumanVietnamese(
  subtitles: { language: string }[],
): boolean {
  return subtitles.some(
    (s) => /^vi/.test(s.language) && s.language !== MACHINE_LANGUAGE,
  )
}
