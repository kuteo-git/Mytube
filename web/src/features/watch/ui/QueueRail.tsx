import clsx from 'clsx'
import { useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import type { QueueItem } from '@/features/watch/application/queue'
import { ThumbnailSurface } from '@/shared/ui/primitives'
import { formatDuration, formatRelative } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'

/**
 * The list being played through, beside the player.
 *
 * Shows position rather than recommendations: when a queue is running, what
 * plays next is already settled, and the useful information is where you are
 * in it and what is coming.
 */
export function QueueRail({
  items,
  currentIndex,
  search,
}: {
  items: QueueItem[]
  currentIndex: number
  search: string
}) {
  const activeRef = useRef<HTMLAnchorElement>(null)

  // Deep into a long queue the playing item would otherwise be off-screen, and
  // the rail would look like it had stopped following along.
  useEffect(() => {
    activeRef.current?.scrollIntoView({ block: 'nearest' })
  }, [currentIndex])

  return (
    <aside className="flex w-full flex-col rounded-xl bg-surface" aria-label="Queue">
      <div className="border-b border-line px-4 py-3">
        <p className="text-sm font-medium">Playing from queue</p>
        <p className="mt-0.5 text-xs text-text-2">
          {currentIndex >= 0 ? `${currentIndex + 1} / ${items.length}` : `${items.length} videos`}
        </p>
      </div>

      <ol className="max-h-[70vh] overflow-y-auto py-1 no-scrollbar">
        {items.map((item, index) => {
          const active = index === currentIndex
          return (
            <li key={item.id}>
              <Link
                ref={active ? activeRef : undefined}
                to={`/watch/${item.id}${search}`}
                aria-current={active ? 'true' : undefined}
                className={clsx(
                  'flex gap-3 px-3 py-2 transition-colors duration-150 ease-out hover:bg-surface-hover',
                  active && 'bg-surface-hover',
                )}
              >
                <span
                  className={clsx(
                    'w-5 shrink-0 self-center text-right text-xs tabular-nums',
                    active ? 'text-text' : 'text-text-2',
                  )}
                >
                  {active ? '▶' : index + 1}
                </span>
                {/* Same anatomy as every other row on the page — thumbnail,
                    duration, title, channel — so a queue does not read as a
                    different kind of list from the one it was opened from. */}
                <div className="w-[100px] shrink-0">
                  <ThumbnailSurface
                    hue={hueFromId(item.id)}
                    src={item.thumbnailUrl}
                    alt={item.title}
                    rounded="rounded-lg"
                  >
                    {item.durationSeconds > 0 && (
                      <span className="absolute right-1 bottom-1 rounded bg-badge px-1 text-[11px] font-medium tabular-nums">
                        {formatDuration(item.durationSeconds)}
                      </span>
                    )}
                  </ThumbnailSurface>
                </div>
                <div className="min-w-0 flex-1">
                  <p className={clsx('clamp-2 text-sm leading-5', active && 'font-medium')}>
                    {item.title}
                  </p>
                  {/* Channel and date on one line, the same shape the grid
                      cards use — a queue should not read as a different kind of
                      list from the one it was opened from. */}
                  {(item.channelName || item.publishedAt) && (
                    <p className="clamp-1 mt-1 text-xs text-text-2">
                      {[item.channelName, item.publishedAt && formatRelative(item.publishedAt)]
                        .filter(Boolean)
                        .join(' · ')}
                    </p>
                  )}
                </div>
              </Link>
            </li>
          )
        })}
      </ol>
    </aside>
  )
}
