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
  label: string
  hint: string
  /** Fallback range, used only until the server publishes its own. */
  min: number
  max: number
  step: number
  /** Turns the stored number into what the slider reads out. */
  format: (value: number) => string
  /** The built-in value, shown so a changed setting is recognisable as changed. */
  fallback: number
  risky?: boolean
}

export const RANKING_FIELDS: RankingField[] = [
  {
    key: 'sessionBlend',
    label: 'Follow what you are watching now',
    hint: 'How much the last few videos of this sitting outweigh your whole watch history. At zero the page ignores today entirely; at the top it is almost the Next rail.',
    min: 0,
    max: 1,
    step: 0.05,
    format: (v) => `${Math.round(v * 100)}% this sitting`,
    fallback: 0.5,
  },
  {
    key: 'freshSubscribedPercent',
    label: 'Room kept for new uploads',
    hint: 'A share of every page reserved for videos your channels published recently, so a new upload never has to win a place on score alone.',
    min: 0,
    max: 40,
    step: 1,
    format: (v) => `${v}% of the page`,
    fallback: 10,
  },
  {
    key: 'freshnessWindowHours',
    label: 'How long a video counts as new',
    hint: 'Both the reserved share above and the boost that surfaces breaking news use this.',
    min: 1,
    max: 24 * 14,
    step: 1,
    format: formatHours,
    fallback: 48,
  },
  {
    key: 'maxPublishedAgeDays',
    label: 'Oldest video the home page will show',
    hint: 'Older videos stay reachable through search and on their channel; they just do not fill the grid.',
    min: 1,
    max: 3650,
    step: 1,
    format: formatDays,
    fallback: 365,
  },
  {
    key: 'recencyHalfLifeDays',
    label: 'How fast newly added videos fade',
    hint: 'A video the library has just fetched leads the grid, then settles. This is how long it takes to lose half of that lift.',
    min: 0.5,
    max: 365,
    step: 0.5,
    format: (v) => `${v} day${v === 1 ? '' : 's'}`,
    fallback: 5,
  },
  {
    key: 'softmaxTemperature',
    label: 'How closely the order follows the score',
    hint: 'Lower keeps the best videos at the top every time; higher lets close scores trade places between visits. Near zero the page looks identical on every refresh.',
    min: 0.05,
    max: 5,
    step: 0.05,
    format: (v) => v.toFixed(2),
    fallback: 0.6,
    risky: true,
  },
  {
    key: 'samplePoolSize',
    label: 'How many videos enter the draw',
    hint: 'Only this many of each share are shuffled; the rest stay in score order. Raising it far is what let videos scoring below zero onto the first page.',
    min: 24,
    max: 480,
    step: 24,
    format: (v) => `${v} videos`,
    fallback: 120,
    risky: true,
  },
]

function formatHours(hours: number): string {
  if (hours < 48) return `${hours} hour${hours === 1 ? '' : 's'}`
  const days = Math.round(hours / 24)
  return `${days} days`
}

function formatDays(days: number): string {
  if (days < 60) return `${days} day${days === 1 ? '' : 's'}`
  if (days < 730) return `${Math.round(days / 30)} months`
  return `${(days / 365).toFixed(1)} years`
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
