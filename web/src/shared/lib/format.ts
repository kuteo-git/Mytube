/**
 * Display formatting helpers — pure functions, framework independent.
 *
 * These carry more English than anything else in the app, and they carry it
 * where it is hardest to see: not as a label somebody wrote once, but baked
 * into a return value that renders on every card in every grid. "248 views",
 * "3 days ago", "4.1M subscribers".
 *
 * Worse, they carry English *grammar*. `formatRelative` appended an "s" for
 * the plural; Vietnamese has no plural, so a translated version that kept the
 * shape would say "3 ngàys trước".
 *
 * So language is a parameter. Still pure — `format.test.ts` tests them without
 * React — and components reach them through `useFormat()`, which binds the
 * current language once instead of every call site remembering to pass it.
 */
import type { Language } from '@/shared/i18n'

/**
 * The abbreviations for thousand, million and billion.
 *
 * Vietnamese uses its own initials rather than the English K/M/B — nghìn,
 * triệu, tỷ — which is what YouTube shows there and what anybody reading a
 * view count expects.
 */
const SCALE: Record<Language, [string, string, string]> = {
  en: ['K', 'M', 'B'],
  vi: ['N', 'Tr', 'T'],
}

/** Words that follow a number. Vietnamese needs no plural form of any of them. */
const WORDS = {
  en: { views: 'views', subscribers: 'subscribers', justNow: 'just now' },
  vi: { views: 'lượt xem', subscribers: 'người đăng ký', justNow: 'vừa xong' },
} satisfies Record<Language, Record<string, string>>

/**
 * Relative time, one entry per unit.
 *
 * Vietnamese has a single word for each and no plural, which is why the
 * English side keeps its "s" here as data rather than as a rule applied to
 * every language.
 */
const RELATIVE = {
  en: {
    year: ['year', 'years'],
    month: ['month', 'months'],
    week: ['week', 'weeks'],
    day: ['day', 'days'],
    hour: ['hour', 'hours'],
    minute: ['minute', 'minutes'],
  },
  vi: {
    year: ['năm', 'năm'],
    month: ['tháng', 'tháng'],
    week: ['tuần', 'tuần'],
    day: ['ngày', 'ngày'],
    hour: ['giờ', 'giờ'],
    minute: ['phút', 'phút'],
  },
} satisfies Record<Language, Record<string, [string, string]>>

/** Where the locale tags live, for the one thing ICU does better than a table. */
const LOCALE: Record<Language, string> = { en: 'en-US', vi: 'vi-VN' }

export function formatViews(n: number, lang: Language = 'en'): string {
  return `${formatCount(n, lang)} ${WORDS[lang].views}`
}

export function formatCount(n: number, lang: Language = 'en'): string {
  const [k, m, b] = SCALE[lang]
  if (n >= 1_000_000_000) return `${trim(n / 1_000_000_000)}${b}`
  if (n >= 1_000_000) return `${trim(n / 1_000_000)}${m}`
  if (n >= 1_000) return `${trim(n / 1_000)}${k}`
  return `${n}`
}

export function formatSubscribers(n: number, lang: Language = 'en'): string {
  return `${formatCount(n, lang)} ${WORDS[lang].subscribers}`
}

function trim(n: number): string {
  return n >= 100 ? String(Math.round(n)) : String(Math.round(n * 10) / 10).replace(/\.0$/, '')
}

export function formatDuration(sec: number): string {
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = Math.floor(sec % 60)
  const pad = (x: number) => String(x).padStart(2, '0')
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`
}

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 31_536_000],
  ['month', 2_592_000],
  ['week', 604_800],
  ['day', 86_400],
  ['hour', 3_600],
  ['minute', 60],
]

export function formatRelative(iso: string, lang: Language = 'en'): string {
  const diff = (Date.now() - new Date(iso).getTime()) / 1000
  for (const [unit, secs] of UNITS) {
    if (diff >= secs) {
      const value = Math.floor(diff / secs)
      const [one, many] = RELATIVE[lang][unit as keyof (typeof RELATIVE)['en']]
      const word = value > 1 ? many : one
      // Vietnamese puts the marker after the phrase — "3 ngày trước" — and
      // English before the unit. Two shapes, not one shape with a translated
      // word, which is the thing that makes a translation read as a machine's.
      return lang === 'vi' ? `${value} ${word} trước` : `${value} ${word} ago`
    }
  }
  return WORDS[lang].justNow
}

export function formatDate(iso: string, lang: Language = 'en'): string {
  // ICU rather than a table of month names: it already knows that Vietnamese
  // writes 22 thg 8, 2026, and beating it at that would mean maintaining a
  // list to arrive at the same answer.
  return new Date(iso).toLocaleDateString(LOCALE[lang], {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

export function formatBytes(bytes: number): string {
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`
  if (bytes >= 1024 ** 2) return `${Math.round(bytes / 1024 ** 2)} MB`
  return `${Math.round(bytes / 1024)} KB`
}
