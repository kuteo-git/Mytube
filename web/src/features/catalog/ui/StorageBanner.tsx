import { Info, X } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { formatBytes } from '@/shared/lib/format'

/**
 * Dismissible alert banner. On youtube.com this slot carries billing notices;
 * here it surfaces the constraint that actually matters for this project —
 * remaining disk budget and the LRU eviction that kicks in (CLAUDE.md §4).
 */
export function StorageBanner({
  usedBytes,
  budgetBytes,
}: {
  usedBytes: number
  budgetBytes: number
}) {
  const [dismissed, setDismissed] = useState(false)
  if (dismissed) return null

  const ratio = usedBytes / budgetBytes
  if (ratio < 0.75) return null

  return (
    <div className="flex items-start gap-3 py-3">
      <Info size={20} className="mt-0.5 shrink-0 text-text-2" />
      <div className="flex-1">
        <p className="text-sm">
          Storage is {Math.round(ratio * 100)}% full ({formatBytes(usedBytes)} of{' '}
          {formatBytes(budgetBytes)}). The least recently watched videos will be removed from
          disk automatically; their metadata and history are kept.
        </p>
        <Link
          to="/storage"
          className="mt-2 inline-block text-sm font-medium text-link hover:underline"
        >
          Manage storage
        </Link>
      </div>
      <button
        type="button"
        aria-label="Dismiss"
        onClick={() => setDismissed(true)}
        className="grid h-9 w-9 shrink-0 place-items-center rounded-full hover:bg-surface-hover"
      >
        <X size={20} />
      </button>
    </div>
  )
}
