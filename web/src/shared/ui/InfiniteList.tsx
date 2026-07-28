import { Loader2 } from 'lucide-react'
import { useEffect, useRef } from 'react'

/**
 * Loads the next page when the end of the list comes into view, and also keeps
 * a real button there.
 *
 * The button is not a fallback for old browsers — it is how this works with a
 * keyboard and, later, a TV remote. Scroll-triggered loading is invisible to
 * anyone who is not scrolling with a pointer, so a list that only auto-loads
 * simply ends for them.
 */
export function InfiniteList({
  hasMore,
  isLoading,
  onLoadMore,
}: {
  hasMore: boolean
  isLoading: boolean
  onLoadMore: () => void
}) {
  const sentinelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const element = sentinelRef.current
    if (!element || !hasMore || isLoading) return

    const observer = new IntersectionObserver(
      ([entry]) => {
        if (entry.isIntersecting) onLoadMore()
      },
      // Start fetching before the end is actually reached, so the grid keeps
      // filling rather than stalling at the bottom.
      { rootMargin: '600px' },
    )

    observer.observe(element)
    return () => observer.disconnect()
  }, [hasMore, isLoading, onLoadMore])

  if (!hasMore && !isLoading) return null

  return (
    <div ref={sentinelRef} className="flex justify-center py-10">
      {isLoading ? (
        <span className="flex items-center gap-2 text-sm text-text-2">
          <Loader2 size={18} className="animate-spin" />
          Loading more
        </span>
      ) : (
        <button
          type="button"
          onClick={onLoadMore}
          className="h-9 rounded-full bg-surface px-5 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
        >
          Load more
        </button>
      )}
    </div>
  )
}
