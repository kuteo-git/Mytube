import {
  type ViewRect,
  fullRectMobile,
  lerpRect,
  miniRectDesktop,
  miniRectMobile,
} from './player-geometry'

/**
 * Where the player host should be right now, and in which coordinate space.
 *
 * The coordinate space is not a detail. On desktop the full-size player is
 * `absolute` in document coordinates, which is what lets the browser scroll it
 * with the page for free — no scroll listener, and therefore nothing that can
 * lag a frame behind the rest of the page. The miniplayer is `fixed`, because it
 * must not scroll away. Those are the only two options and they are not
 * interchangeable.
 */
export interface HostPlacement {
  position: 'absolute' | 'fixed'
  rect: ViewRect
  animate: boolean
}

export interface PlacementInput {
  mode: 'hidden' | 'full' | 'mini'
  isMobile: boolean
  /** The watch page slot, in document coordinates. Null until measured. */
  slotDocRect: ViewRect | null
  viewport: { width: number; height: number }
  scrollY: number
  /** 0..1 while a finger is dragging the player down, otherwise null. */
  dragProgress: number | null
}

/**
 * What scrolling the slot in or out of view should do to the pinned state.
 *
 * The `dismissed` flag is what makes the miniplayer's close button mean
 * something on the watch page. Without it, closing the miniplayer and then
 * scrolling one more line brings straight back the thing that was just put away,
 * which makes the button all but dead.
 *
 * It expires on the viewer looking at the player again rather than on a timer.
 * A flag cleared by a clock can be stuck for as long as the clock says; one
 * cleared by scrolling back to the player cannot be stuck at all, because the
 * only way to reach it is to have already got what you wanted.
 */
export function resolvePin(
  isIntersecting: boolean,
  dismissed: boolean,
): { pinned: boolean; dismissed: boolean } {
  if (isIntersecting) return { pinned: false, dismissed: false }
  if (dismissed) return { pinned: false, dismissed: true }
  return { pinned: true, dismissed: false }
}

/**
 * Which shape the player is in.
 *
 * Derived rather than stored. The previous version kept a separate `flipping`
 * state that had to be entered and left by hand, and a transition that failed to
 * announce its own end left the player stranded in it — invisible, still
 * playing. There is no state here that something has to remember to clear.
 */
export function deriveMode(
  hasState: boolean,
  isWatch: boolean,
  pinnedMini: boolean,
): 'hidden' | 'full' | 'mini' {
  if (!hasState) return 'hidden'
  return isWatch && !pinnedMini ? 'full' : 'mini'
}

/** The full-size placement, in whichever space that mode uses. */
export function fullPlacement(input: PlacementInput): HostPlacement {
  const { isMobile, slotDocRect, viewport } = input
  if (isMobile) {
    return { position: 'fixed', rect: fullRectMobile(viewport.width), animate: true }
  }
  // Before the slot has been measured there is nowhere to be. Callers treat a
  // null slot as "stay put", which is why this never invents a rectangle.
  return {
    position: 'absolute',
    rect: slotDocRect ?? { top: 0, left: 0, width: 0, height: 0 },
    animate: true,
  }
}

export function miniPlacement(input: PlacementInput): HostPlacement {
  const { isMobile, viewport } = input
  return {
    position: 'fixed',
    rect: isMobile
      ? miniRectMobile(viewport.width, viewport.height)
      : miniRectDesktop(viewport.width, viewport.height),
    animate: true,
  }
}

/** Document coordinates to viewport coordinates. */
export function toViewport(rect: ViewRect, scrollY: number): ViewRect {
  return { ...rect, top: rect.top - scrollY }
}

/**
 * The placement for a given moment.
 *
 * A drag outranks the mode: while a finger is down the host follows the finger
 * between the two rectangles, with the transition off so it tracks exactly
 * rather than easing towards where the finger was a moment ago.
 */
export function placementFor(input: PlacementInput): HostPlacement | null {
  if (input.mode === 'hidden') return null

  const mini = miniPlacement(input)
  const full = fullPlacement(input)

  // Full size is a position on the watch page, so until that page has been
  // measured there is no such position to move to. Answering with the empty
  // rectangle would send the player to the top-left corner at zero size and then
  // out to wherever the slot turned out to be — an extra leg of movement that
  // corresponds to nothing the viewer did. Waiting in the corner costs a frame
  // and animates once, correctly.
  if (input.mode === 'full' && !input.isMobile && !input.slotDocRect) {
    return miniPlacement(input)
  }

  if (input.dragProgress !== null) {
    // Drags only happen on mobile, where full is already `fixed`, so both ends
    // of the interpolation are in the same space and lerping is meaningful.
    return {
      position: 'fixed',
      rect: lerpRect(full.rect, mini.rect, input.dragProgress),
      animate: false,
    }
  }

  return input.mode === 'full' ? full : mini
}

/**
 * Whether moving between two placements needs an intermediate frame.
 *
 * CSS cannot transition across a change of `position`: switching from
 * `absolute` to `fixed` in the same frame as the coordinates change makes the
 * element jump to its destination with no animation. The fix is to first commit
 * a frame that is already `fixed` but still *looks* identical — same pixels on
 * screen, expressed in the new space — and only then animate. This reports when
 * that bridging frame is required.
 */
export function needsBridge(from: HostPlacement | null, to: HostPlacement | null): boolean {
  if (!from || !to) return false
  return from.position !== to.position
}

/** The bridging frame: `to`'s coordinate space, `from`'s on-screen position. */
export function bridgePlacement(
  from: HostPlacement,
  to: HostPlacement,
  scrollY: number,
): HostPlacement {
  const rect =
    to.position === 'fixed' && from.position === 'absolute'
      ? toViewport(from.rect, scrollY)
      : { ...from.rect, top: from.rect.top + scrollY }
  return { position: to.position, rect, animate: false }
}
