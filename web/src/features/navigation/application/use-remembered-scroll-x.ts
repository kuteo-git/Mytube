import { useCallback, useRef } from 'react'

/**
 * Keep a horizontal scroller where it was left.
 *
 * The vertical position of a page is remembered by useScrollRestoration, but a
 * row that scrolls sideways inside that page is its own scroller with its own
 * position, and nothing was keeping it. Switching tabs unmounts the page, so the
 * topic chips came back scrolled to the beginning — and on a library with a
 * dozen topics the chip you had scrolled to is exactly the one you were using.
 *
 * Stored under a name rather than derived from the route, because a sideways
 * row is a component's own state and there may be several on a page.
 * `sessionStorage` for the same reason the vertical positions use it: a reload
 * should not lose the place you were in.
 */
export function useRememberedScrollX(storageKey: string) {
  const elRef = useRef<HTMLDivElement | null>(null)

  const attach = useCallback(
    (el: HTMLDivElement | null) => {
      const previous = elRef.current
      if (previous) previous.onscroll = null
      elRef.current = el
      if (!el) return

      // Set during commit, so it is in place before the first paint — assigning
      // it in an effect would show one frame at the beginning and then jump.
      try {
        const raw = window.sessionStorage.getItem(storageKey)
        if (raw !== null) el.scrollLeft = Number(raw)
      } catch {
        // Private mode. A row that starts at the beginning is not a failure.
      }

      // `onscroll` rather than addEventListener, so that a callback ref firing
      // twice — which React does in development — cannot leave two listeners
      // on one element. Assignment replaces; adding accumulates.
      el.onscroll = () => {
        try {
          window.sessionStorage.setItem(storageKey, String(el.scrollLeft))
        } catch {
          /* nothing worth failing a scroll over */
        }
      }
    },
    [storageKey],
  )

  return { ref: elRef, attach }
}
