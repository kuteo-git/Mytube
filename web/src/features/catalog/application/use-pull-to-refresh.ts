import { useEffect, useRef, useState } from 'react'
import {
  REFRESH_THRESHOLD,
  canPull,
  pullDistance,
  shouldRefresh,
} from './pull-to-refresh'

/**
 * Wire the pull gesture to a scroller.
 *
 * Touch events rather than pointer events, and that is not a preference: the
 * browser's own elastic bounce has to be called off, and calling it off means
 * `preventDefault` on `touchmove`, which only a **non-passive** listener may
 * do. React attaches its own listeners passively, so this cannot be JSX props —
 * it has to be `addEventListener` with the flag spelled out.
 *
 * Only ever from the top of the scroller, and only for one finger. Two fingers
 * on a page is a zoom, and taking that would be a worse theft than any refresh
 * is worth.
 */
export function usePullToRefresh({
  scroller,
  enabled,
  onRefresh,
}: {
  scroller: HTMLElement | null
  enabled: boolean
  onRefresh: () => Promise<unknown>
}) {
  /** How far the page has been pulled, or null when nobody is pulling. */
  const [distance, setDistance] = useState<number | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const startY = useRef<number | null>(null)
  const refreshingRef = useRef(false)
  /**
   * The distance as of the last move, written where it is computed.
   *
   * Not derived from the state above. The listeners are attached once, so they
   * would read whatever the state was when React last rendered — and between a
   * `touchmove` and the `touchend` that follows it there may have been no
   * render at all. On a real screen the two are frames apart and it would
   * usually work, which is the worst way for it to be wrong.
   */
  const distanceRef = useRef(0)

  useEffect(() => {
    if (!enabled || !scroller) return

    const onStart = (e: TouchEvent) => {
      if (refreshingRef.current || e.touches.length !== 1) return
      // The player is not part of the page.
      //
      // It is rendered inside the scroller so its `absolute` placement travels
      // with the content, which also puts it within reach of this listener.
      // Dragging the picture down to put it away is its own gesture, and the
      // page was answering it too — two answers to one movement, and a page has
      // no business responding to a drag aimed at something on top of it.
      if ((e.target as Element | null)?.closest?.('[data-player-host]')) return
      // Recorded only from the top. Deciding later, when the finger has already
      // moved, would mean a scroll that happened to end at the top could turn
      // into a pull halfway through.
      startY.current = canPull(scroller.scrollTop) ? e.touches[0].clientY : null
    }

    const onMove = (e: TouchEvent) => {
      const from = startY.current
      if (from === null) return
      const dy = e.touches[0].clientY - from

      if (dy <= 0) {
        // Pulled back up past where it started: hand the gesture back rather
        // than holding on to it. From here the finger is scrolling.
        distanceRef.current = 0
        setDistance(null)
        startY.current = null
        return
      }
      // The browser would otherwise bounce the scroller at the same time, so
      // the page would be pulled twice by one finger.
      if (e.cancelable) e.preventDefault()
      distanceRef.current = pullDistance(dy)
      setDistance(distanceRef.current)
    }

    const onEnd = () => {
      const pulled = startY.current === null ? null : distanceRef.current
      startY.current = null
      distanceRef.current = 0
      if (pulled === null || !shouldRefresh(pulled)) {
        setDistance(null)
        return
      }
      // Held at the threshold rather than at wherever the finger was: the
      // indicator settles into the place it will spin from, so the transition
      // from "let go" to "working" is one movement instead of a jump.
      refreshingRef.current = true
      setRefreshing(true)
      setDistance(null)
      void onRefresh().finally(() => {
        refreshingRef.current = false
        setRefreshing(false)
      })
    }

    scroller.addEventListener('touchstart', onStart, { passive: true })
    scroller.addEventListener('touchmove', onMove, { passive: false })
    scroller.addEventListener('touchend', onEnd)
    scroller.addEventListener('touchcancel', onEnd)
    return () => {
      scroller.removeEventListener('touchstart', onStart)
      scroller.removeEventListener('touchmove', onMove)
      scroller.removeEventListener('touchend', onEnd)
      scroller.removeEventListener('touchcancel', onEnd)
    }
  }, [enabled, scroller, onRefresh])

  return {
    distance: distance ?? 0,
    pulling: distance !== null,
    refreshing,
    /**
     * How far the page should be held down right now.
     *
     * The finger's distance while pulling, and the threshold while working —
     * the content stays open around the spinner instead of snapping shut over
     * it the instant the gesture is released. It is also what keeps the
     * indicator on screen at all: it lives just above the content's top edge,
     * so a page back at zero would put it behind the top bar.
     */
    offset: refreshing ? REFRESH_THRESHOLD : (distance ?? 0),
  }
}
