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

/**
 * The mode a `<track>` must be in, given everything that has a say in it.
 *
 * One definition, used everywhere a mode is written: the pass that applies the
 * preference, the handover between the two video elements, and the listener
 * that puts it back when something else changes it. They used to disagree —
 * only the handover kept Vietnamese alive for the narrator — and a mode written
 * in three places with two meanings is a mode nobody can predict.
 *
 * - In the bar there are no subtitles at all: drawn proportionally to a 128px
 *   picture they are a few illegible pixels over the only part of it there is.
 *   The preference is untouched, so they return the moment it is a picture.
 * - Vietnamese is never `disabled` while reading aloud, because a disabled
 *   track has its VTT load cancelled and the voice stops mid-sentence. Hidden
 *   keeps the cues without drawing them.
 */
export function desiredTrackMode(input: {
  trackLanguage: string
  captions: string | null
  bar: boolean
  narrationOn: boolean
}): 'showing' | 'hidden' | 'disabled' {
  const wanted = !input.bar && input.trackLanguage === input.captions
  if (wanted) return 'showing'
  const isVi =
    input.trackLanguage === 'vi' || input.trackLanguage === 'vie'
  if (isVi && input.narrationOn) return 'hidden'
  return 'disabled'
}

/** One row of the t('ui.subtitles') setting, past the t('ui.off') that always leads it. */
export type SubtitleOption = { value: string; label: string; hint: string }

/**
 * The subtitle choices this video actually offers.
 *
 * Computed rather than rendered inline, because whether there are any is what
 * decides if the setting is shown at all. A row reading "Off" with nothing to
 * turn on is a dead control, and §5 has no dead controls — which is what a
 * video with no captions, or with only captions in languages nobody here reads,
 * used to get.
 */
export function subtitleOptions(
  subtitles: { language: string; label: string; generated: boolean }[],
): SubtitleOption[] {
  const hasVi = hasHumanVietnamese(subtitles)
  const hasEn = subtitles.some((s) => /^en/.test(s.language))
  const options: SubtitleOption[] = subtitles
    .filter((t) => /^(en|eng|vi|vie|vi-x-mt)$/.test(t.language))
    .map((t) => ({
      value: t.language,
      label:
        t.language === MACHINE_LANGUAGE
          ? 'VI (auto)'
          : /^en/.test(t.language)
            ? 'EN'
            : 'VI',
      hint: t.label + (t.generated ? ' (auto-generated)' : ''),
    }))
  // Offered before it exists, when it is the viewer who can bring it into
  // existence: choosing it is what starts the translation, and the gateway only
  // attaches the track once a file has been written. Only where it could be
  // produced — English to work from, and no Vietnamese written by a person.
  if (!subtitles.some((t) => t.language === MACHINE_LANGUAGE) && hasEn && !hasVi) {
    options.push({
      value: MACHINE_LANGUAGE,
      label: 'VI (auto)',
      // The track's own name is content, read in the language it is written in,
      // as the gateway's machineVTTLabel already is. The rest is UI copy.
      hint: 'queue.machineVietnamese',
    })
  }
  return options
}
