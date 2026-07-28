/** Display formatting helpers — pure functions, framework independent. */

export function formatViews(n: number): string {
  if (n >= 1_000_000_000) return `${trim(n / 1_000_000_000)}B views`
  if (n >= 1_000_000) return `${trim(n / 1_000_000)}M views`
  if (n >= 1_000) return `${trim(n / 1_000)}K views`
  return `${n} views`
}

export function formatCount(n: number): string {
  if (n >= 1_000_000) return `${trim(n / 1_000_000)}M`
  if (n >= 1_000) return `${trim(n / 1_000)}K`
  return `${n}`
}

export function formatSubscribers(n: number): string {
  return `${formatCount(n)} subscribers`
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

export function formatRelative(iso: string): string {
  const diff = (Date.now() - new Date(iso).getTime()) / 1000
  for (const [unit, secs] of UNITS) {
    if (diff >= secs) {
      const value = Math.floor(diff / secs)
      return `${value} ${unit}${value > 1 ? 's' : ''} ago`
    }
  }
  return 'just now'
}

export function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString('en-US', {
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
