/**
 * The shape of the equaliser, with no audio in sight.
 *
 * Everything here is arithmetic on numbers the viewer set: which bands exist,
 * what a preset means in decibels, and what the limits are. It holds no
 * `AudioContext` and imports no React, so the interesting part — "does this
 * preset actually say what it claims" — is testable without an audio clock, the
 * same split `narration-levels.ts` uses for volume.
 */

export type BandKind = 'lowshelf' | 'peaking' | 'highshelf'

export interface Band {
  frequency: number
  kind: BandKind
  /** Short label for the slider, as it appears under the column. */
  label: string
}

/**
 * Ten ISO bands, and two of them are not peaking filters.
 *
 * A peaking filter at 32 Hz leaves everything below roughly 25 Hz untouched,
 * because a bell has two sides — so "bass boost" would lift a narrow band and
 * leave the floor exactly where it was. The classic ten-band layout ends in
 * shelves for that reason: the bottom slider raises everything beneath it and
 * the top slider everything above.
 */
export const BANDS: readonly Band[] = [
  { frequency: 32, kind: 'lowshelf', label: '32' },
  { frequency: 64, kind: 'peaking', label: '64' },
  { frequency: 125, kind: 'peaking', label: '125' },
  { frequency: 250, kind: 'peaking', label: '250' },
  { frequency: 500, kind: 'peaking', label: '500' },
  { frequency: 1000, kind: 'peaking', label: '1k' },
  { frequency: 2000, kind: 'peaking', label: '2k' },
  { frequency: 4000, kind: 'peaking', label: '4k' },
  { frequency: 8000, kind: 'peaking', label: '8k' },
  { frequency: 16000, kind: 'highshelf', label: '16k' },
]

/**
 * Bandwidth of the peaking filters.
 *
 * √2 is the value that makes adjacent one-octave bands meet near their
 * half-power points: wider and every slider drags its neighbours with it, so the
 * ten controls behave like three; narrower and boosting a band rings rather than
 * shaping. The shelves ignore Q entirely.
 */
export const BAND_Q = Math.SQRT2

/** Furthest a band may be pushed, in either direction. */
export const MAX_BAND_DB = 12

/** The preamp only ever attenuates — see `PRESETS` below for why. */
export const MIN_PREAMP_DB = -12
export const MAX_PREAMP_DB = 0

export interface EqSettings {
  enabled: boolean
  /** One gain per entry in `BANDS`, in decibels. */
  gains: number[]
  /** Output trim in decibels, at most 0. */
  preamp: number
  /** Which preset these values came from, or `custom` once edited. */
  preset: PresetName
}

export type PresetName = 'flat' | 'bass' | 'vocal' | 'treble' | 'loudness' | 'custom'

export interface Preset {
  name: Exclude<PresetName, 'custom'>
  label: string
  gains: number[]
  preamp: number
}

/**
 * Five presets, and every one of them carries its own preamp.
 *
 * Boosting bands adds gain to a signal already mastered close to full scale, and
 * the sum of several positive bands clips — which is heard as crackle, not as
 * loudness. Each preset therefore ships with the attenuation that pays for its
 * own boosts, which is why the preamp's range stops at 0: it exists to make room,
 * never to add more.
 *
 * There is no Podcast preset. It would be Vocal under a second name, and a list
 * long enough to scroll is a list nobody reads to the end of.
 */
export const PRESETS: readonly Preset[] = [
  { name: 'flat', label: 'Flat', gains: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0], preamp: 0 },
  { name: 'bass', label: 'Bass Boost', gains: [6, 5, 4, 2, 0, 0, 0, 0, 0, 0], preamp: -6 },
  { name: 'vocal', label: 'Vocal', gains: [-2, -2, -1, 0, 2, 3, 3, 2, 0, 0], preamp: -3 },
  { name: 'treble', label: 'Treble', gains: [0, 0, 0, 0, 0, 1, 2, 4, 5, 5], preamp: -5 },
  {
    name: 'loudness',
    label: 'Loudness',
    gains: [6, 5, 3, 0, -2, -2, 0, 3, 5, 6],
    preamp: -6,
  },
]

export function clampBand(db: number): number {
  if (!Number.isFinite(db)) return 0
  return Math.min(MAX_BAND_DB, Math.max(-MAX_BAND_DB, db))
}

export function clampPreamp(db: number): number {
  if (!Number.isFinite(db)) return 0
  return Math.min(MAX_PREAMP_DB, Math.max(MIN_PREAMP_DB, db))
}

export function presetByName(name: PresetName): Preset | undefined {
  return PRESETS.find((p) => p.name === name)
}

/** The settings a preset stands for, ready to be applied. */
export function settingsForPreset(name: Exclude<PresetName, 'custom'>): EqSettings {
  const preset = presetByName(name) ?? PRESETS[0]
  return {
    enabled: true,
    gains: [...preset.gains],
    preamp: preset.preamp,
    preset: preset.name,
  }
}

export const FLAT: EqSettings = {
  enabled: false,
  gains: BANDS.map(() => 0),
  preamp: 0,
  preset: 'flat',
}

/**
 * Force a settings object into range, whatever it came from.
 *
 * Written for values read back out of `localStorage`, which is a place another
 * version of this app wrote to and a place the viewer can edit by hand. A gains
 * array of the wrong length is the case that matters: the band list may grow
 * later, and a stored array from before that must not leave filters unset.
 */
export function normaliseSettings(input: Partial<EqSettings> | null | undefined): EqSettings {
  if (!input) return { ...FLAT, gains: [...FLAT.gains] }
  const gains = BANDS.map((_, i) => clampBand(Number(input.gains?.[i] ?? 0)))
  const preset: PresetName =
    input.preset && (input.preset === 'custom' || presetByName(input.preset))
      ? input.preset
      : 'custom'
  return {
    enabled: Boolean(input.enabled),
    gains,
    preamp: clampPreamp(Number(input.preamp ?? 0)),
    preset,
  }
}

/** Whether these gains still match the preset they claim, after an edit. */
export function matchesPreset(settings: EqSettings, name: Exclude<PresetName, 'custom'>): boolean {
  const preset = presetByName(name)
  if (!preset) return false
  return (
    preset.preamp === settings.preamp &&
    preset.gains.every((g, i) => g === settings.gains[i])
  )
}

/**
 * The preset these values correspond to, or `custom`.
 *
 * Called after every slider move so the picker follows the sliders rather than
 * the other way round: dragging a band back onto Flat's shape should say Flat,
 * not leave the panel insisting the settings are custom when they are not.
 */
export function identifyPreset(settings: EqSettings): PresetName {
  const hit = PRESETS.find((p) => matchesPreset(settings, p.name))
  return hit ? hit.name : 'custom'
}

/** Decibels as a linear gain multiplier, for the preamp node. */
export function dbToGain(db: number): number {
  return Math.pow(10, db / 20)
}
