import { useEffect, useLayoutEffect, useRef } from 'react'
import { useLocation, useNavigationType } from 'react-router-dom'
import { type NavKind, canReach, isTabRoot, scrollTargetFor } from './scroll-restoration'

/**
 * Two records, because two questions are being asked.
 *
 * By history entry answers "where was I when I left this exact screen", which
 * is what a back button needs. By path answers "where does this tab live", which
 * is what returning to a tab needs — and those differ: a tab returned to is
 * usually a *new* history entry with no memory of its own.
 */
const ENTRY_PREFIX = 'yt-scroll:'
const PATH_PREFIX = 'yt-scroll-path:'

/**
 * Frames to keep trying a restore for.
 *
 * Returning to a feed needs the grid rebuilt before there is anywhere to scroll
 * to; the query cache makes that quick but not synchronous. Twenty frames is
 * about a third of a second — long enough for a cached page, short enough that
 * a page which genuinely got shorter settles rather than fighting a scroll the
 * viewer has since started themselves.
 */
const RESTORE_FRAMES = 20

function read(prefix: string, key: string): number | undefined {
  try {
    const raw = window.sessionStorage.getItem(prefix + key)
    return raw === null ? undefined : Number(raw)
  } catch {
    return undefined
  }
}

function write(prefix: string, key: string, y: number) {
  try {
    window.sessionStorage.setItem(prefix + key, String(y))
  } catch {
    // Private mode, or a full quota. Losing a scroll position is not worth
    // failing a navigation over.
  }
}

/**
 * Give the app a phone's scrolling manners.
 *
 * Kept in `sessionStorage` rather than in a module variable so a reload does
 * not wipe every position — history survives a reload, and a back button that
 * works before one and not after is worse than one that never worked.
 */
/**
 * An off switch, for telling this hook apart from everything else on the page.
 *
 * `sessionStorage.setItem('yt-scroll-off', '1')` and reload: navigation stops
 * scrolling entirely and the app behaves as it did before any of this existed.
 * It is here because a flicker reported against the top bar could be this hook
 * moving the viewport, or something else moving it, and no amount of reading
 * the code settles which — one reload does.
 */
function disabled(): boolean {
  try {
    return window.sessionStorage.getItem('yt-scroll-off') === '1'
  } catch {
    return false
  }
}

export function useScrollRestoration() {
  const { key, pathname } = useLocation()
  const navigationType = useNavigationType() as NavKind
  const currentRef = useRef({ key, pathname })
  const previousPathRef = useRef<string | null>(null)
  /** What the layout effect decided, for the after-paint check below. */
  const targetRef = useRef<number | null>(null)

  // Take the browser's own restoration out of the argument. Left on automatic
  // it also restores on POP, a frame or two after this does and from its own
  // idea of the position — so the page would land, then jump.
  useEffect(() => {
    const previous = window.history.scrollRestoration
    window.history.scrollRestoration = 'manual'
    return () => {
      window.history.scrollRestoration = previous
    }
  }, [])

  // Record where this screen is being left, under both keys. Written
  // continuously rather than on the way out: React unmounts the old page before
  // a cleanup could read a meaningful offset, and by then the window has often
  // already moved.
  useEffect(() => {
    currentRef.current = { key, pathname }
    let frame = 0
    const onScroll = () => {
      if (frame) return
      // Both the position and the screen it belongs to are read *now* and
      // carried into the frame. Reading them when the frame fires was a real
      // bug waiting to happen: a scroll on the way out of a page schedules a
      // frame that lands after the router has already moved on, and the old
      // page's offset would be filed under the new page's name.
      const at = currentRef.current
      const y = window.scrollY
      frame = window.requestAnimationFrame(() => {
        frame = 0
        write(ENTRY_PREFIX, at.key, y)
        write(PATH_PREFIX, at.pathname, y)
      })
    }
    window.addEventListener('scroll', onScroll, { passive: true })
    return () => {
      window.removeEventListener('scroll', onScroll)
      if (frame) window.cancelAnimationFrame(frame)
    }
  }, [key, pathname])

  // A layout effect, not an ordinary one, and the difference is visible.
  //
  // `useEffect` runs after the browser has painted, so the new page got one
  // frame at the offset the old one left behind — or at zero, where a shorter
  // page had the browser clamp it — and only then jumped to where it belonged.
  // With a sticky top bar that intermediate frame is not subtle: the bar is
  // repositioned twice in two frames and reads as a flicker on every tab
  // switch. Running before paint puts the scroll in the same frame as the
  // content, so there is only ever one position to see.
  //
  // The retry loop below still paints early when the content genuinely is not
  // there yet; nothing can scroll to an offset a page does not have.
  useLayoutEffect(() => {
    if (disabled()) return
    const samePath = previousPathRef.current === pathname
    previousPathRef.current = pathname

    const target = scrollTargetFor({
      kind: navigationType,
      tabRoot: isTabRoot(pathname),
      samePath,
      savedForEntry: read(ENTRY_PREFIX, key),
      savedForPath: read(PATH_PREFIX, pathname),
    })
    targetRef.current = target
    if (target === null) return

    // Already there. Worth checking rather than scrolling anyway: on a phone
    // every scroll can slide the browser's own address bar in or out, and that
    // resizes the viewport and shifts the sticky top bar. Two tabs both at the
    // top — the ordinary case — would otherwise pay for a scroll that changes
    // nothing and be seen doing it.
    if (window.scrollY === target) return

    // Instantly, never smoothly. The player host converts between fixed and
    // absolute positioning by reading window.scrollY during a layout effect
    // (player-context.tsx), and a scroll still in motion would have it read a
    // number that is about to be wrong — the miniplayer jumps, then animates.
    if (target === 0) {
      window.scrollTo(0, 0)
      return
    }

    let frames = 0
    let raf = 0
    const attempt = () => {
      if (
        canReach(target, document.documentElement.scrollHeight, window.innerHeight)
      ) {
        window.scrollTo(0, target)
        return
      }
      // Still shorter than the offset it is being asked for, which on a feed
      // means the rows are arriving. Landing now would put it at the bottom of
      // a half-built page — indistinguishable from the position having been
      // forgotten.
      if (++frames < RESTORE_FRAMES) raf = window.requestAnimationFrame(attempt)
    }
    attempt()
    return () => {
      if (raf) window.cancelAnimationFrame(raf)
    }
  }, [key, pathname, navigationType])

  // Say it once more after paint, and only if something moved.
  //
  // Moving the restore before paint cost the ordering that used to make it the
  // last word: a parent's layout effect runs *before* a child's ordinary
  // effect, so a page that scrolls itself on mount now lands after this and
  // wins. HomePage did exactly that, and this pairing is what stops the next
  // one being a silent regression.
  //
  // Normally a no-op — the position is already the target, and nothing is
  // called — so it adds no second scroll and no second flicker of its own.
  useEffect(() => {
    if (disabled()) return
    const target = targetRef.current
    if (target === null || window.scrollY === target) return
    if (!canReach(target, document.documentElement.scrollHeight, window.innerHeight)) {
      return
    }
    window.scrollTo(0, target)
  }, [key, pathname, navigationType])
}
