import type { Dictionary, TranslationKey } from '@/shared/i18n/en'
/**
 * The ranking constants the household can move without a rebuild.
 *
 * Every field is optional, and absent means "whatever the ranker was built
 * with". Zero is a real answer for several of these — a session blend of zero
 * says "ignore what I am watching right now" — so a missing field and a field
 * set to zero have to stay different things all the way down to the file.
 *
 * Deliberately not every weight in the ranker. Two dozen sliders would trade the
 * one advantage this heuristic has over a model — that every number can be
 * explained — for a control panel nobody can account for six months later.
 */
export interface RankingSettings {
  sessionBlend?: number
  freshSubscribedPercent?: number
  freshnessWindowHours?: number
  maxPublishedAgeDays?: number
  recencyHalfLifeDays?: number
  softmaxTemperature?: number
  samplePoolSize?: number
}

export type RankingKey = keyof RankingSettings

/**
 * What each setting is, in the terms somebody moving it would use.
 *
 * `risky` marks the two that are guard rails rather than tastes. They are here
 * because they are genuinely useful to experiment with, and marked because both
 * can rebuild a measured failure: entering a whole several-thousand-video bucket
 * into the draw put eight videos scoring below zero on the first page of
 * twenty-four, and a temperature near zero freezes the feed into one order that
 * never changes between visits.
 */
export interface RankingField {
  key: RankingKey
  label: TranslationKey
  hint: TranslationKey
  /** Fallback range, used only until the server publishes its own. */
  min: number
  max: number
  step: number
  /** Turns the stored number into what the slider reads out. */
  /**
   * How to say this value: a translation key and the number to put in it.
   *
   * Not a finished string. The unit words — hours, days, months, videos —
   * belong in the dictionary, and returning them from here would mean this
   * domain file holding English, which is also the file that cannot call a
   * hook.
   */
  format: (value: number) => Formatted
  /** The built-in value, shown so a changed setting is recognisable as changed. */
  fallback: number
  risky?: boolean
}

/** A translation key and the number it interpolates. */
/**
 * A unit key and the number it interpolates.
 *
 * Narrowed to the unit keys rather than to every key that exists, and not for
 * tidiness: `t` refuses a union whose members take different interpolation
 * options, and every key here takes exactly `{{value}}`. Saying so is what
 * makes the call compile without a cast.
 */
export type Formatted = [key: UnitKey, value: number]

type UnitKey = `settings.ranking.unit.${keyof Dictionary['settings']['ranking']['unit']}`

export const RANKING_FIELDS: RankingField[] = [
  {
    key: 'sessionBlend',
    label: 'settings.ranking.sessionBlend.label',
    hint: 'settings.ranking.sessionBlend.hint',
    min: 0,
    max: 1,
    step: 0.05,
    format: (v) => ['settings.ranking.unit.thisSitting', Math.round(v * 100)],
    fallback: 0.5,
  },
  {
    key: 'freshSubscribedPercent',
    label: 'settings.ranking.freshSubscribed.label',
    hint: 'settings.ranking.freshSubscribed.hint',
    min: 0,
    max: 40,
    step: 1,
    format: (v) => ['settings.ranking.unit.ofPage', v],
    fallback: 10,
  },
  {
    key: 'freshnessWindowHours',
    label: 'settings.ranking.freshnessWindow.label',
    hint: 'settings.ranking.freshnessWindow.hint',
    min: 1,
    max: 24 * 14,
    step: 1,
    format: formatHours,
    fallback: 48,
  },
  {
    key: 'maxPublishedAgeDays',
    label: 'settings.ranking.maxAge.label',
    hint: 'settings.ranking.maxAge.hint',
    min: 1,
    max: 3650,
    step: 1,
    format: formatDays,
    fallback: 365,
  },
  {
    key: 'recencyHalfLifeDays',
    label: 'settings.ranking.recencyHalfLife.label',
    hint: 'settings.ranking.recencyHalfLife.hint',
    min: 0.5,
    max: 365,
    step: 0.5,
    format: (v) => ['settings.ranking.unit.days', v],
    fallback: 5,
  },
  {
    key: 'softmaxTemperature',
    label: 'settings.ranking.temperature.label',
    hint: 'settings.ranking.temperature.hint',
    min: 0.05,
    max: 5,
    step: 0.05,
    format: (v) => ['settings.ranking.unit.plain', v],
    fallback: 0.6,
    risky: true,
  },
  {
    key: 'samplePoolSize',
    label: 'settings.ranking.poolSize.label',
    hint: 'settings.ranking.poolSize.hint',
    min: 24,
    max: 480,
    step: 24,
    format: (v) => ['settings.ranking.unit.videos', v],
    fallback: 120,
    risky: true,
  },
]

// Which unit reads best at this magnitude, and the number to say it with.
//
// A key and a value rather than a finished string: the unit words live in the
// dictionary, where "day"/"days" is data instead of a rule. Applied as a rule
// to Vietnamese — which has no plural — it produces "5 ngàys".
function formatHours(hours: number): Formatted {
  if (hours < 48) return ['settings.ranking.unit.hours', hours]
  return ['settings.ranking.unit.days', Math.round(hours / 24)]
}

function formatDays(days: number): Formatted {
  if (days < 60) return ['settings.ranking.unit.days', days]
  if (days < 730) return ['settings.ranking.unit.months', Math.round(days / 30)]
  return ['settings.ranking.unit.years', Number((days / 365).toFixed(1))]
}

/** What a field reads when the household has not set it. */
export function valueOf(settings: RankingSettings, field: RankingField): number {
  const set = settings[field.key]
  return set === undefined ? field.fallback : set
}

/** Whether anything at all has been overridden. */
export function isUnset(settings: RankingSettings): boolean {
  return RANKING_FIELDS.every((f) => settings[f.key] === undefined)
}

/**
 * Set one field, or clear it back to the built-in value.
 *
 * Clearing writes `undefined` rather than the fallback number, and the
 * difference matters: a field left unset follows the ranker if the ranker ever
 * changes, while one pinned to today's default does not.
 */
export function setField(
  settings: RankingSettings,
  key: RankingKey,
  value: number | undefined,
): RankingSettings {
  const next = { ...settings }
  if (value === undefined) delete next[key]
  else next[key] = value
  return next
}

export function sameSettings(a: RankingSettings, b: RankingSettings): boolean {
  return RANKING_FIELDS.every((f) => a[f.key] === b[f.key])
}
