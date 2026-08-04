import { useCallback, useEffect, useState } from 'react'

/**
 * How many rows a group on the Activity page shows before it is asked for more.
 *
 * Ten, because the page's job is to be read at a glance: the answer to "is
 * anything wrong" should be visible without scrolling past a month of
 * successful downloads to find it.
 */
export const ACTIVITY_PAGE_SIZE = 10

/**
 * Reveal a long list a page at a time, from an array already in hand.
 *
 * Client-side, and deliberately so for the download groups: all three come back
 * in a single request — the split into failed, running and finished happens
 * here — so paging them at the server would mean three queries and three
 * cursors to keep in step, which is a lot of machinery for a diagnostics page
 * on a household library where the queue rarely passes a few dozen.
 *
 * The scan history is the opposite case and is paged at the server: it grows by
 * a row an hour, forever.
 */
export function usePagedList<T>(items: T[], pageSize = ACTIVITY_PAGE_SIZE) {
  const [shown, setShown] = useState(pageSize)

  // A list that shrinks under an expanded view — a job dismissed, a download
  // finishing and moving groups — must not leave the count stranded above what
  // there is, or "View more" would offer rows that are not there.
  useEffect(() => {
    setShown((current) => Math.max(pageSize, Math.min(current, Math.max(items.length, pageSize))))
  }, [items.length, pageSize])

  const showMore = useCallback(() => setShown((n) => n + pageSize), [pageSize])

  return {
    visible: items.slice(0, shown),
    remaining: Math.max(0, items.length - shown),
    showMore,
  }
}
