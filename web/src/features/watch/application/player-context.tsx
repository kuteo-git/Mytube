import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import type { MediaState, SubtitleTrack } from '@/features/catalog/domain/video'
import { forgetLastWatched } from './last-watched'
import { useLocation } from 'react-router-dom'
import {
  BOTTOM_NAV_HEIGHT,
  MINI_MARGIN,
  MOBILE_BREAKPOINT,
  type ViewRect,
  miniRectDesktop,
  miniRectMobile,
} from './player-geometry'
import {
  type HostPlacement,
  bridgePlacement,
  deriveMode,
  dragFraction,
  draggingPlacement,
  needsBridge,
  placementFor,
  resolvePin,
} from './player-host'

/**
 * The Player's state, held above the router so it survives navigation.
 *
 * The Player is mounted exactly once, inside a host element in AppShell that is
 * never re-parented. That constraint is the whole design. An earlier version
 * portal'd the Player between the watch page and a corner container, which does
 * not move the DOM: React tears the subtree out of the old container and builds
 * a fresh one in the new, `<video>` included. A rebuilt `<video>` has no buffer,
 * no currentTime and no play state, while the old one keeps playing, detached
 * and invisible — audio with no picture. Nothing about the animation could have
 * fixed that, so the host moves and the Player never does.
 */
export interface PlayerState {
  videoId: string
  title: string
  hue: number
  durationSeconds: number
  initialPositionSeconds: number
  mediaState: MediaState
  subtitles: SubtitleTrack[]
  thumbnailURL?: string
  channelTitle?: string
  nextVideoTitle?: string
  onPlayNext?: () => void
  /** False when the player is being restored from a previous visit. */
  autoplay?: boolean
}

export type PlayerMode = 'hidden' | 'full' | 'mini'

export interface PlayerContextValue {
  state: PlayerState | null
  mode: PlayerMode
  isMobile: boolean
  /** The home indicator's share of the bottom edge; zero without one. */
  safeBottom: number
  /** Whether the viewer is on a watch page. Decides what "close" means. */
  isWatch: boolean
  /**
   * Whether the app's own bars are drawn at all.
   *
   * False on the phone's drill-in screens — the watch layer and a channel —
   * which carry their own chrome. Held here rather than in AppShell because the
   * *player's* geometry depends on it: the bar sits above the navigation, and
   * on a screen with no navigation it was left floating a tab bar's height off
   * the bottom of the screen. One fact in one place, or the shell and the
   * player disagree about where the bottom of the screen is.
   */
  chromeHidden: boolean
  /** Where and how AppShell should position the player host. Null when hidden. */
  placement: HostPlacement | null
  /**
   * How much room the foot of a page needs for the miniplayer.
   *
   * Available whether or not one is showing, so a page can leave the space
   * before the player arrives rather than reflowing once it has.
   */
  miniReserve: number
  /** Callback ref for the watch page's layout slot. */
  slotRef: (el: HTMLDivElement | null) => void
  /**
   * Callback ref for the element that scrolls, and the element itself.
   *
   * The page scrolls inside `<main>` rather than the window, so that the
   * browser's own address bar never collapses or reappears. On a phone that
   * collapse resizes the viewport, and everything pinned to the top edge is
   * repositioned with it — which is what a restored scroll position looked
   * like: a flicker of the top bar on every tab switch.
   *
   * Everything here that used to read `window.scrollY` reads this instead. The
   * two coordinate spaces are the same shape, so the arithmetic is unchanged;
   * only what "the document" means has moved inward by one element.
   */
  scrollerRef: (el: HTMLElement | null) => void
  scrollerEl: HTMLElement | null
  activate: (state: PlayerState) => void
  deactivate: () => void
  /** Puts the player into the corner while staying on the watch page. */
  minimize: () => void
  /** Brings it back to full size. */
  restore: () => void
  /**
   * Closing the miniplayer while the watch page is still open: back to its slot,
   * paused, and no re-pinning until the viewer scrolls up to it again.
   */
  dismiss: () => void
  /**
   * Increments whenever playback should stop. A token rather than a flag because
   * pausing twice has to be able to happen twice.
   */
  pauseToken: number
  /**
   * How far the viewer has dragged the player towards the corner, or null when
   * nobody is dragging.
   *
   * Held here rather than in the Player because the thing that moves is the
   * host, and the host's position is this provider's business. The Player reads
   * the finger; this decides what that means for the rectangle.
   */
  dragOffset: number | null
  setDragOffset: (pixels: number | null) => void
  /** 0..1 through the journey, for anything that has to fade with it. */
  dragFraction: number
}

const PlayerContext = createContext<PlayerContextValue | null>(null)

export function usePlayer(): PlayerContextValue {
  const ctx = useContext(PlayerContext)
  if (!ctx) throw new Error('usePlayer must be used within PlayerProvider')
  return ctx
}

function readViewport() {
  return { width: window.innerWidth, height: window.innerHeight }
}

/**
 * The home indicator's share of the bottom edge.
 *
 * Read back from the variable the stylesheet sets, because the layout
 * arithmetic here is plain TypeScript and `env()` only exists inside CSS. Zero
 * on everything without a home indicator — and also zero on an iPhone if the
 * viewport meta tag loses `viewport-fit=cover`, which is the quiet way this
 * whole allowance stops working.
 */
function readSafeInset(name: '--safe-bottom' | '--safe-top'): number {
  if (typeof getComputedStyle !== 'function') return 0
  const raw = getComputedStyle(document.documentElement).getPropertyValue(name)
  return Number.parseFloat(raw) || 0
}

export function PlayerProvider({
  children,
  isWatch,
}: {
  children: React.ReactNode
  isWatch: boolean
}) {
  const { pathname } = useLocation()
  const [state, setState] = useState<PlayerState | null>(null)
  const [viewport, setViewport] = useState(readViewport)
  const [safeBottom, setSafeBottom] = useState(() => readSafeInset('--safe-bottom'))
  const [safeTop, setSafeTop] = useState(() => readSafeInset('--safe-top'))
  const [slotEl, setSlotEl] = useState<HTMLDivElement | null>(null)
  const [scrollerEl, setScrollerEl] = useState<HTMLElement | null>(null)
  const [slotDocRect, setSlotDocRect] = useState<ViewRect | null>(null)
  const [pinnedMini, setPinnedMini] = useState(false)
  const [dismissed, setDismissed] = useState(false)
  const [pauseToken, setPauseToken] = useState(0)
  const [dragOffset, setDragOffset] = useState<number | null>(null)

  // The observer callback needs the current dismissal, but rebuilding the
  // observer to capture it would be worse than useless: a fresh observer fires
  // an initial callback against a slot that is still out of view, which pins the
  // player again — the exact state the viewer just closed. A mirror ref keeps
  // the callback current without the observer ever being torn down.
  const dismissedRef = useRef(false)
  dismissedRef.current = dismissed

  const isMobile = viewport.width < MOBILE_BREAKPOINT

  // A channel gets the same bare treatment as the watch layer: its own back
  // bar, no search bar, no tab bar. Keyed on the route so a channel reached
  // from a video's byline behaves like one reached from Subscriptions.
  const chromeHidden = isMobile && (isWatch || pathname.startsWith('/channel/'))

  /** What the navigation takes from the bottom edge — nothing where it is not drawn. */
  const navHeight = chromeHidden ? 0 : BOTTOM_NAV_HEIGHT

  // A callback ref rather than a plain one, and not for style. A plain ref is
  // assigned during commit, which is after render — so reading it during render
  // yields the previous commit's value, and nothing re-renders when it changes.
  // Returning to the watch page therefore read `null` for the slot on the very
  // render that needed it. A callback ref sets state, so the measurement and the
  // render that uses it stay in step.
  const slotRef = useCallback((el: HTMLDivElement | null) => setSlotEl(el), [])
  const scrollerRef = useCallback((el: HTMLElement | null) => setScrollerEl(el), [])

  useEffect(() => {
    // Rotating the phone changes which edge the home indicator is on, so the
    // inset is re-read alongside the viewport rather than measured once.
    const onResize = () => {
      setViewport(readViewport())
      setSafeBottom(readSafeInset('--safe-bottom'))
      setSafeTop(readSafeInset('--safe-top'))
    }
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  // Measure the slot in document coordinates. Document rather than viewport is
  // what lets the full-size player be `absolute` and scroll with the page for
  // free — no scroll listener, so nothing can lag behind the page it sits in.
  useLayoutEffect(() => {
    if (!slotEl) {
      setSlotDocRect(null)
      return
    }
    const measure = () => {
      const r = slotEl.getBoundingClientRect()
      // Relative to the scroller's content, which is what `absolute` means for
      // the host: it is a child of that same element, so the two share an
      // origin and the browser scrolls them together with no listener of ours
      // in the path. Before the page scrolled inside an element this was the
      // document, and the sum was the same shape — viewport rect plus however
      // far the thing had been scrolled.
      const base = scrollerEl?.getBoundingClientRect()
      setSlotDocRect({
        top: r.top - (base?.top ?? 0) + (scrollerEl?.scrollTop ?? 0),
        left: r.left - (base?.left ?? 0) + (scrollerEl?.scrollLeft ?? 0),
        width: r.width,
        height: r.height,
      })
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(slotEl)
    window.addEventListener('resize', measure)
    // The slot's position within the scroller does not change as it scrolls, so
    // this does not listen for scrolling — the same reason there was never a
    // scroll listener here before.
    return () => {
      observer.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [slotEl, scrollerEl])

  // Scroll past the player and it folds into the corner, as it does on youtube.
  // An IntersectionObserver rather than a scroll handler: the browser works out
  // the crossing itself, off the main thread, so there is no per-frame work in
  // the scroll path and nothing to jitter. Mobile pins the player instead, and
  // minimises by gesture, so this only applies to the desktop shell.
  useEffect(() => {
    if (!slotEl || isMobile || !isWatch) return
    const observer = new IntersectionObserver(
      ([entry]) => {
        const next = resolvePin(entry.isIntersecting, dismissedRef.current)
        setPinnedMini(next.pinned)
        setDismissed(next.dismissed)
      },
      { threshold: 0 },
    )
    observer.observe(slotEl)
    return () => observer.disconnect()
  }, [slotEl, isMobile, isWatch])

  const activate = useCallback((next: PlayerState) => {
    setPinnedMini(false)
    setDismissed(false)
    setState(next)
  }, [])

  const deactivate = useCallback(() => {
    // Closing it is the answer to the offer. It must not be made again.
    forgetLastWatched()
    setState(null)
    setPinnedMini(false)
    setDismissed(false)
  }, [])

  const minimize = useCallback(() => {
    setPinnedMini(true)
  }, [])

  const restore = useCallback(() => {
    setPinnedMini(false)
    setDismissed(false)
  }, [])

  // Closing the miniplayer while the watch page is open is a request to put the
  // small window away, not to stop watching — the video still has a home on the
  // page. So it goes back to its slot rather than being destroyed. Deliberately
  // without scrolling: the viewer is reading further down and moving them would
  // be answering a question they did not ask. It pauses because a player that is
  // out of view and still audible is the very fault this whole feature exists to
  // avoid, and it would be no better for being intentional.
  const dismiss = useCallback(() => {
    setPinnedMini(false)
    setDismissed(true)
    setPauseToken((t) => t + 1)
  }, [])

  const miniReserve = useMemo(() => {
    const rect = isMobile
      ? miniRectMobile(viewport.width, viewport.height, navHeight + safeBottom)
      : miniRectDesktop(viewport.width, viewport.height)
    return rect.height + MINI_MARGIN * 2 + (isMobile ? navHeight + safeBottom : 0)
  }, [isMobile, viewport, safeBottom, navHeight])

  const mode: PlayerMode = deriveMode(Boolean(state), isWatch, pinnedMini)

  const target = useMemo(() => {
    const input = {
      mode,
      isMobile,
      slotDocRect,
      viewport,
      safeBottom,
      safeTop,
      navHeight,
      scrollY: 0,
    }
    // A drag in flight overrides where the player would otherwise be. Only from
    // full size and only on a phone: the gesture is a request to leave the
    // watch page, and there is nothing to leave from a player already in the
    // corner.
    if (dragOffset !== null && isMobile && mode === 'full') {
      return draggingPlacement(input, dragOffset)
    }
    return placementFor(input)
  }, [mode, isMobile, slotDocRect, viewport, safeBottom, safeTop, navHeight, dragOffset])

  // The same number the rectangle is built from, so the chrome that fades
  // cannot drift out of step with the shape that is moving.
  const dragFractionNow =
    dragOffset === null || !isMobile || mode !== 'full'
      ? 0
      : dragFraction(
          {
            mode,
            isMobile,
            slotDocRect,
            viewport,
            safeBottom,
            safeTop,
            navHeight,
            scrollY: 0,
          },
          dragOffset,
        )

  // CSS cannot transition across a change of `position`, and the two modes do
  // not share a coordinate space: full-size is `absolute` in the document,
  // the miniplayer is `fixed` to the viewport. Moving straight between them
  // makes the player jump with no animation at all. So a single frame is
  // committed first that is already in the destination's space but still shows
  // the same pixels, and only the frame after that animates.
  const [bridge, setBridge] = useState<HostPlacement | null>(null)

  // A bridge into a space the player is no longer heading for is stale, and
  // holding it would strand the host there: while a bridge is showing, the
  // placement it was built from is the one being rendered, so a target that
  // moved on again in the meantime would never be reached. Dropping it the
  // moment the destination space disagrees makes the machine converge on its
  // own rather than depending on a frame arriving to rescue it.
  const staleBridge = bridge !== null && target !== null && bridge.position !== target.position
  const rendered = staleBridge ? target : (bridge ?? target)
  const lastRef = useRef<HostPlacement | null>(null)

  useLayoutEffect(() => {
    if (staleBridge) {
      setBridge(null)
      lastRef.current = rendered
      return
    }
    const previous = lastRef.current
    if (!bridge && needsBridge(previous, target)) {
      setBridge(bridgePlacement(previous!, target!, scrollerEl?.scrollTop ?? 0))
      return
    }
    lastRef.current = rendered
  }, [target, bridge, rendered, staleBridge, scrollerEl])

  useLayoutEffect(() => {
    if (!bridge) return
    const frame = requestAnimationFrame(() => setBridge(null))
    return () => cancelAnimationFrame(frame)
  }, [bridge])

  const value = useMemo<PlayerContextValue>(
    () => ({
      state,
      mode,
      isMobile,
      safeBottom,
      isWatch,
      chromeHidden,
      placement: rendered,
      miniReserve,
      slotRef,
      scrollerRef,
      scrollerEl,
      activate,
      deactivate,
      minimize,
      restore,
      dismiss,
      pauseToken,
      dragOffset,
      setDragOffset,
      dragFraction: dragFractionNow,
    }),
    [
      state,
      mode,
      isMobile,
      safeBottom,
      isWatch,
      chromeHidden,
      rendered,
      miniReserve,
      slotRef,
      scrollerRef,
      scrollerEl,
      activate,
      deactivate,
      minimize,
      restore,
      dismiss,
      pauseToken,
      dragOffset,
      dragFractionNow,
    ],
  )

  return <PlayerContext.Provider value={value}>{children}</PlayerContext.Provider>
}
