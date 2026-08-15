/**
 * The equaliser setting, stored on this device.
 *
 * Per-device rather than per-viewer, and rather than on the server with the feed
 * mix. An equaliser corrects for what the sound comes out of — a laptop speaker
 * that has no bass to speak of, headphones that have too much — so the same
 * person wants different curves on the phone and on the television. Sharing one
 * curve across the house would make the setting wrong nearly everywhere it was
 * read.
 *
 * One key holding one JSON object, unlike `settings-prefs.ts` and its key per
 * value: this is ten numbers that only mean anything together, and half-written
 * state across eleven keys is a curve nobody chose.
 */

import { FLAT, normaliseSettings, type EqSettings } from './eq-presets'

const KEY = 'yt-equalizer-v1'

export function loadEqSettings(): EqSettings {
  try {
    const raw = window.localStorage.getItem(KEY)
    if (!raw) return { ...FLAT, gains: [...FLAT.gains] }
    return normaliseSettings(JSON.parse(raw) as Partial<EqSettings>)
  } catch {
    // Unparseable is the same as absent. Written by an older version, or edited
    // by hand — either way Flat is a setting that cannot be wrong, and refusing
    // to start over a stored string would take the sound out with it.
    return { ...FLAT, gains: [...FLAT.gains] }
  }
}

export function saveEqSettings(settings: EqSettings) {
  window.localStorage.setItem(KEY, JSON.stringify(settings))
}
