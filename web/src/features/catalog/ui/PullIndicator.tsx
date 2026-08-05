import clsx from 'clsx'
import { ArrowDown, Loader2 } from 'lucide-react'
import { pullProgress } from '../application/pull-to-refresh'

/**
 * What a pull looks like while it is happening.
 *
 * It says what letting go will do *before* it is let go — the arrow turns over
 * as the threshold is reached — which is the difference between a gesture that
 * can be abandoned and one that can only be regretted.
 *
 * Sits just above the content's top edge and travels with it, so the pull
 * reveals it from under the top bar the way a phone does. It does NOT compute
 * its own offset: the content is already being moved by the pull, and adding
 * the distance again here counted it twice — the arrow raced ahead of the gap
 * and ended up over the chip row, which is where it was reported hidden.
 *
 * Above the chip row in the stack (`z-20` against its `z-10`) so a sticky bar
 * arriving at the top edge cannot cover it either, and below the top bar, which
 * is what it emerges from.
 */
export function PullIndicator({
  distance,
  refreshing,
}: {
  distance: number
  refreshing: boolean
}) {
  if (!refreshing && distance <= 0) return null

  const progress = pullProgress(distance)
  const armed = progress >= 1

  return (
    <div
      className="pointer-events-none absolute inset-x-0 top-0 z-20 flex -translate-y-full
                 justify-center pb-2"
      aria-live="polite"
      aria-label={refreshing ? 'Refreshing' : undefined}
    >
      <div className="grid h-9 w-9 place-items-center rounded-full bg-surface shadow-lg">
        {refreshing ? (
          <Loader2 size={18} className="animate-spin text-text-2" />
        ) : (
          <ArrowDown
            size={18}
            className={clsx(
              'transition-[transform,color] duration-150 ease-out',
              armed ? 'rotate-180 text-text' : 'text-text-2',
            )}
            // Fades in with the pull, so a gesture abandoned early leaves
            // nothing behind that has to be explained.
            style={{ opacity: Math.min(progress * 1.5, 1) }}
          />
        )}
      </div>
    </div>
  )
}
