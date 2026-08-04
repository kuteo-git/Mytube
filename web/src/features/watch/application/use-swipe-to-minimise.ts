import { type PointerEvent as ReactPointerEvent, useCallback, useRef } from 'react'
import { isVerticalDrag, shouldCommit, velocityOf } from './player-gesture'

/**
 * Drag the full-size player down to put it away.
 *
 * The decisions all live in player-gesture.ts, which is pure and tested. What
 * is here is the part that cannot be: reading a pointer, and knowing when it
 * has said enough to be believed.
 *
 * Only touch, and only from full size on a phone. A mouse has a close button
 * and a back button; a drag would be a second way to do a thing that already
 * has an obvious one, and it would take click-to-pause with it.
 */
export function useSwipeToMinimise({
  enabled,
  height,
  onDrag,
  onCommit,
}: {
  enabled: boolean
  /** The player's current height in pixels — what the finger is moving. */
  height: () => number
  /** How many pixels down the finger has come, or null when it lets go. */
  onDrag: (pixels: number | null) => void
  onCommit: () => void
}) {
  // `performance.now()` rather than `event.timeStamp`. The event's own clock has
  // a different origin in different browsers — and in jsdom it is read-only and
  // always zero, so a test could not tell a flick from a resting finger.
  const now = () => performance.now()

  const start = useRef<{ x: number; y: number; at: number; id: number } | null>(null)
  const dragging = useRef(false)
  /**
   * The last two samples, so a release has a segment to measure speed over.
   *
   * One is not enough, and that is not a subtlety: `pointerup` arrives at the
   * same place and very nearly the same moment as the final `pointermove`, so
   * measuring from that move to the release reports a speed of zero — every
   * flick would be read as a stop. Keeping the sample before it means the
   * closing speed is measured across movement that actually happened.
   */
  const prev = useRef<{ y: number; at: number }>({ y: 0, at: 0 })
  const last = useRef<{ y: number; at: number }>({ y: 0, at: 0 })

  const reset = useCallback(() => {
    start.current = null
    dragging.current = false
    onDrag(null)
  }, [onDrag])

  const down = useCallback(
    (e: ReactPointerEvent) => {
      if (!enabled || e.pointerType !== 'touch') return
      const at = now()
      start.current = { x: e.clientX, y: e.clientY, at, id: e.pointerId }
      prev.current = { y: e.clientY, at }
      last.current = { y: e.clientY, at }
      dragging.current = false
    },
    [enabled],
  )

  const move = useCallback(
    (e: ReactPointerEvent) => {
      const from = start.current
      if (!from || e.pointerId !== from.id) return

      const dx = e.clientX - from.x
      const dy = e.clientY - from.y

      if (!dragging.current) {
        // Nothing has happened yet. A tap is what shows and hides the controls
        // on touch, so until the movement is unambiguously a downward drag this
        // gesture must stay out of the way entirely.
        if (!isVerticalDrag(dx, dy)) return
        dragging.current = true
        // Taken now, so the drag keeps receiving events even when the finger
        // leaves the player — which it does immediately, since the player is
        // shrinking away from underneath it.
        e.currentTarget.setPointerCapture?.(from.id)
      }

      prev.current = last.current
      last.current = { y: e.clientY, at: now() }
      onDrag(dy)
    },
    [onDrag],
  )

  const up = useCallback(
    (e: ReactPointerEvent) => {
      const from = start.current
      if (!from || !dragging.current) {
        start.current = null
        return
      }

      const dy = e.clientY - from.y
      // Measured over the tail of the gesture rather than the whole of it: a
      // slow drag that ends in a flick is a flick, and averaging from the
      // beginning would report it as slow. Taken from the sample before the
      // last one, because the release itself lands where the last move did.
      const velocity = velocityOf(e.clientY - prev.current.y, now() - prev.current.at)

      if (shouldCommit({ dy, playerHeight: height(), velocity })) {
        // Held at the far end of the journey rather than released here. The
        // rectangle there is the corner itself, so whatever replaces this once
        // the navigation lands is already in the same place — there is no frame
        // in which the player is somewhere else.
        onDrag(Number.MAX_SAFE_INTEGER)
        start.current = null
        dragging.current = false
        onCommit()
        return
      }
      reset()
    },
    [height, onCommit, onDrag, reset],
  )

  return { down, move, up, cancel: reset }
}
