/**
 * How this device sounds: the equaliser and the room, together.
 *
 * Per-device rather than per-viewer, and rather than on the server with the feed
 * mix. An equaliser corrects for what the sound comes out of — a laptop speaker
 * with no bass, headphones with too much — so the same person wants different
 * curves on the phone and on the television. Sharing one across the house would
 * make the setting wrong nearly everywhere it was read.
 *
 * One key holding one object, unlike `settings-prefs.ts` and its key per value.
 * These are a dozen numbers that only mean anything together, and half-written
 * state across a dozen keys is a sound nobody chose.
 */

import { FLAT, normaliseSettings, type EqSettings } from './eq-presets'
import { normaliseReverb, REVERB_OFF, type ReverbSettings } from './reverb-presets'

/**
 * The key is still the equaliser's, and deliberately.
 *
 * The reverb arrived later and the stored shape grew a level to hold it, but a
 * new key would have thrown away every curve already saved on every device — for
 * no gain beyond a tidier name. `read` below understands both shapes.
 */
const KEY = 'yt-equalizer-v1'

export interface AudioSettings {
  eq: EqSettings
  reverb: ReverbSettings
}

export const DEFAULT_AUDIO: AudioSettings = {
  eq: { ...FLAT, gains: [...FLAT.gains] },
  reverb: { ...REVERB_OFF },
}

function defaults(): AudioSettings {
  return {
    eq: { ...FLAT, gains: [...FLAT.gains] },
    reverb: { ...REVERB_OFF },
  }
}

/**
 * Accepts what is stored now and what was stored before the reverb existed.
 *
 * The old shape was the equaliser's own object at the top level — recognisable
 * by carrying `gains` where the new shape carries `eq`. Read rather than
 * discarded, because the alternative is silently resetting a curve somebody
 * spent time on.
 */
function read(raw: unknown): AudioSettings {
  if (!raw || typeof raw !== 'object') return defaults()
  const input = raw as Record<string, unknown>
  if (Array.isArray(input.gains)) {
    return {
      eq: normaliseSettings(input as Partial<EqSettings>),
      reverb: { ...REVERB_OFF },
    }
  }
  return {
    eq: normaliseSettings(input.eq as Partial<EqSettings> | undefined),
    reverb: normaliseReverb(input.reverb as Partial<ReverbSettings> | undefined),
  }
}

export function loadAudioSettings(): AudioSettings {
  try {
    const stored = window.localStorage.getItem(KEY)
    if (!stored) return defaults()
    return read(JSON.parse(stored))
  } catch {
    // Unparseable is the same as absent. Written by an older version, or edited
    // by hand — either way the defaults cannot be wrong, and refusing to start
    // over a stored string would take the sound out with it.
    return defaults()
  }
}

export function saveAudioSettings(settings: AudioSettings) {
  window.localStorage.setItem(KEY, JSON.stringify(settings))
}
