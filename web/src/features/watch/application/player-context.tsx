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
import { MOBILE_BREAKPOINT, type ViewRect, dragProgress, shouldCommit } from './player-geometry'
import {
  type HostPlacement,
  bridgePlacement,
  deriveMode,
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
  /** Where and how AppShell should position the player host. Null when hidden. */
  placement: HostPlacement | null
  /** Callback ref for the watch page's layout slot. */
  slotRef: (el: HTMLDivElement | null) => void
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
  /** Reports an in-progress downward drag on the player surface. */
  onDrag: (deltaY: number, playerHeight: number) => void
  /** Ends a drag; commits to the miniplayer or springs back. */
  onDragEnd: (velocity: number) => void
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
function readSafeBottom(): number {
  if (typeof getComputedStyle !== 'function') return 0
  const raw = getComputedStyle(document.documentElement).getPropertyValue('--safe-bottom')
  return Number.parseFloat(raw) || 0
}

export function PlayerProvider({
  children,
  isWatch,
}: {
  children: React.ReactNode
  isWatch: boolean
}) {
  const [state, setState] = useState<PlayerState | null>(null)
  const [viewport, setViewport] = useState(readViewport)
  const [safeBottom, setSafeBottom] = useState(readSafeBottom)
  const [slotEl, setSlotEl] = useState<HTMLDivElement | null>(null)
  const [slotDocRect, setSlotDocRect] = useState<ViewRect | null>(null)
  const [pinnedMini, setPinnedMini] = useState(false)
  const [drag, setDrag] = useState<number | null>(null)
  const [dismissed, setDismissed] = useState(false)
  const [pauseToken, setPauseToken] = useState(0)

  // The observer callback needs the current dismissal, but rebuilding the
  // observer to capture it would be worse than useless: a fresh observer fires
  // an initial callback against a slot that is still out of view, which pins the
  // player again — the exact state the viewer just closed. A mirror ref keeps
  // the callback current without the observer ever being torn down.
  const dismissedRef = useRef(false)
  dismissedRef.current = dismissed

  const isMobile = viewport.width < MOBILE_BREAKPOINT

  // A callback ref rather than a plain one, and not for style. A plain ref is
  // assigned during commit, which is after render — so reading it during render
  // yields the previous commit's value, and nothing re-renders when it changes.
  // Returning to the watch page therefore read `null` for the slot on the very
  // render that needed it. A callback ref sets state, so the measurement and the
  // render that uses it stay in step.
  const slotRef = useCallback((el: HTMLDivElement | null) => setSlotEl(el), [])

  useEffect(() => {
    // Rotating the phone changes which edge the home indicator is on, so the
    // inset is re-read alongside the viewport rather than measured once.
    const onResize = () => {
      setViewport(readViewport())
      setSafeBottom(readSafeBottom())
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
      setSlotDocRect({
        top: r.top + window.scrollY,
        left: r.left + window.scrollX,
        width: r.width,
        height: r.height,
      })
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(slotEl)
    window.addEventListener('resize', measure)
    return () => {
      observer.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [slotEl])

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
    setDrag(null)
    setDismissed(false)
    setState(next)
  }, [])

  const deactivate = useCallback(() => {
    setState(null)
    setPinnedMini(false)
    setDrag(null)
    setDismissed(false)
  }, [])

  const minimize = useCallback(() => {
    setDrag(null)
    setPinnedMini(true)
  }, [])

  const restore = useCallback(() => {
    setDrag(null)
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
    setDrag(null)
    setPinnedMini(false)
    setDismissed(true)
    setPauseToken((t) => t + 1)
  }, [])

  const onDrag = useCallback((deltaY: number, playerHeight: number) => {
    setDrag(dragProgress(deltaY, playerHeight))
  }, [])

  const onDragEnd = useCallback(
    (velocity: number) => {
      const committed = shouldCommit(drag ?? 0, velocity)
      setDrag(null)
      setPinnedMini(committed)
    },
    [drag],
  )

  const mode: PlayerMode = deriveMode(Boolean(state), isWatch, pinnedMini)

  const target = useMemo(
    () =>
      placementFor({
        mode,
        isMobile,
        slotDocRect,
        viewport,
        safeBottom,
        scrollY: 0,
        dragProgress: drag,
      }),
    [mode, isMobile, slotDocRect, viewport, safeBottom, drag],
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
      setBridge(bridgePlacement(previous!, target!, window.scrollY))
      return
    }
    lastRef.current = rendered
  }, [target, bridge, rendered, staleBridge])

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
      placement: rendered,
      slotRef,
      activate,
      deactivate,
      minimize,
      restore,
      dismiss,
      pauseToken,
      onDrag,
      onDragEnd,
    }),
    [
      state,
      mode,
      isMobile,
      safeBottom,
      isWatch,
      rendered,
      slotRef,
      activate,
      deactivate,
      minimize,
      restore,
      dismiss,
      pauseToken,
      onDrag,
      onDragEnd,
    ],
  )

  return <PlayerContext.Provider value={value}>{children}</PlayerContext.Provider>
}
