import type { NarrationEngine } from '@/features/watch/infrastructure/narration-cache'

/**
 * What narration does with a translated cue: nothing, show it, speak it, or
 * both. Separate from which engine produced it, so the two can be judged one at
 * a time — a bad impression from a single combined list of presets could not be
 * traced back to the engine or to the presentation.
 */
export type NarrationOutput = 'off' | 'subs' | 'voice' | 'both'

const ENGINE_KEY = 'yt-narration-engine-v1'
const OUTPUT_KEY = 'yt-narration-output-v1'
const LEGACY_KEY = 'yt-narration-on'

const ENGINES: NarrationEngine[] = ['omniroute', 'nllb', 'qwen']
const OUTPUTS: NarrationOutput[] = ['off', 'subs', 'voice', 'both']

export function loadNarrationPrefs(): {
  engine: NarrationEngine
  output: NarrationOutput
} {
  const rawEngine = window.localStorage.getItem(ENGINE_KEY)
  const rawOutput = window.localStorage.getItem(OUTPUT_KEY)

  const engine = ENGINES.includes(rawEngine as NarrationEngine)
    ? (rawEngine as NarrationEngine)
    : 'omniroute'

  let output: NarrationOutput = 'off'
  if (OUTPUTS.includes(rawOutput as NarrationOutput)) {
    output = rawOutput as NarrationOutput
  } else if (window.localStorage.getItem(LEGACY_KEY) === '1') {
    // Someone who had the old switch on wanted a voice, not subtitles.
    output = 'voice'
  }
  return { engine, output }
}

export function saveNarrationPrefs(p: {
  engine: NarrationEngine
  output: NarrationOutput
}) {
  window.localStorage.setItem(ENGINE_KEY, p.engine)
  window.localStorage.setItem(OUTPUT_KEY, p.output)
}
