import type { ReactNode } from 'react'
import clsx from 'clsx'

/**
 * The name of a page, in the one place it belongs.
 *
 * On a phone a bare screen already carries its name in the back bar (see
 * `bare-screens.ts`), so drawing it again below is the same word twice. On a
 * desktop there is no back bar and the heading is the only name there is.
 *
 * Both halves were being decided per page and had drifted into three shapes:
 * Saved printed its name twice on a phone; Playlists and Watch later printed it
 * nowhere on a desktop after the back bar was given to them; Storage and
 * Activity used a different container again. The visible symptom was the one
 * reported — the padding above the first row of content not matching between
 * pages — because the heading was also what supplied that padding.
 *
 * `spacer` is what keeps the space when the heading itself is gone. A page
 * whose container already has top padding of its own does not want it.
 */
export function PageHeading({
  children,
  spacer = true,
}: {
  children: ReactNode
  /** False where the page's own container already pads the top. */
  spacer?: boolean
}) {
  return (
    <>
      {spacer && <div className="h-4 min-[700px]:hidden" aria-hidden />}
      <h1
        className={clsx(
          'hidden text-2xl font-bold min-[700px]:block',
          spacer && 'py-4',
        )}
      >
        {children}
      </h1>
    </>
  )
}
