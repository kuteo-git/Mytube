/**
 * What narration does with a translated cue: nothing, show it, speak it, or
 * both. Separate from which engine produced it, so the two can be judged one at
 * a time — a bad impression from a single combined list of presets could not be
 * traced back to the engine or to the presentation.
 */
export type NarrationOutput = 'off' | 'subs' | 'voice' | 'both'

const OUTPUT_KEY = 'yt-narration-output-v1'
const AUTO_KEY = 'yt-narration-auto-translate-v1'
const LEGACY_KEY = 'yt-narration-on'

const OUTPUTS: NarrationOutput[] = ['off', 'subs', 'voice', 'both']

export function loadNarrationPrefs(): {
  output: NarrationOutput
  /** Whether the background translation pass may run at all. */
  autoTranslate: boolean
} {
  const rawOutput = window.localStorage.getItem(OUTPUT_KEY)

  let output: NarrationOutput = 'off'
  if (OUTPUTS.includes(rawOutput as NarrationOutput)) {
    output = rawOutput as NarrationOutput
  } else if (window.localStorage.getItem(LEGACY_KEY) === '1') {
    // Someone who had the old switch on wanted a voice, not subtitles.
    output = 'voice'
  }
  // Defaults on: a video with only English subtitles cannot be narrated without
  // it, and someone switching narration on has already said what they want.
  const autoTranslate = window.localStorage.getItem(AUTO_KEY) !== '0'
  return { output, autoTranslate }
}

export function saveNarrationPrefs(p: {
  output: NarrationOutput
  autoTranslate: boolean
}) {
  window.localStorage.setItem(OUTPUT_KEY, p.output)
  window.localStorage.setItem(AUTO_KEY, p.autoTranslate ? '1' : '0')
}
