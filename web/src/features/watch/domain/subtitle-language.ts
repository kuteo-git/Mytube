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

/**
 * Whether the caption list can be trusted to answer "does this video have
 * Vietnamese".
 *
 * The watch page renders as soon as the catalogue row exists, which for a video
 * being opened for the first time is before its captions have been fetched at
 * all. Deciding anything from the list at that moment is deciding from an empty
 * one — and the answer it gives, "no Vietnamese here", is the answer that costs
 * money: the translator starts, and the real Vietnamese track arrives a few
 * seconds later to sit beside a translation nobody needed.
 *
 * Ingest publishes every language in a single write once both caption passes
 * have finished, so one track present means all of them are. The machine track
 * is excluded because it is written by this very pass — counting it would make
 * the pass its own evidence that captions had arrived.
 */
export function captionsSettled(subtitles: { language: string }[]): boolean {
  return subtitles.some((s) => s.language !== MACHINE_LANGUAGE)
}
