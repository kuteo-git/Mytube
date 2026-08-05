/**
 * Where a page should be scrolled to when you arrive on it.
 *
 * The app scrolls the window, and a single-page router never reloads the
 * document — so without this the offset simply stays where the last page left
 * it. Opening Settings from halfway down Home began halfway down Settings, and
 * on a short page that is past the end of it, which reads as a blank screen.
 *
 * Phones set the expectation, and it is three rules rather than one. A tab bar
 * and a navigation stack are different things and remember differently:
 *
 *  - **switching tabs** returns each tab to where you left it. Home scrolled
 *    halfway, over to History, back to Home — still halfway. This is the one a
 *    plain "scroll to top on navigate" gets wrong, and it is the most common
 *    movement in the app.
 *  - **drilling in** to a video or a channel starts at the top, because it is a
 *    screen you have not seen rather than one you are returning to.
 *  - **going back** returns to the exact entry you came from.
 *
 * The first two are both forward navigation, which is why the destination has
 * to be classified rather than the direction alone being consulted.
 */
export type NavKind = 'PUSH' | 'POP' | 'REPLACE'

/**
 * Paths that behave like tabs: somewhere you return to rather than arrive at.
 *
 * The five in the bottom bar and the six in the sidebar, plus topic pages —
 * picking a topic chip is browsing the same feed through a filter, and coming
 * back to a topic you had scrolled through should not start again at the top.
 */
export function isTabRoot(pathname: string): boolean {
  return TAB_ROOTS.has(pathname) || pathname.startsWith('/topic/')
}

const TAB_ROOTS = new Set([
  '/',
  '/subscriptions',
  '/saved',
  '/history',
  '/storage',
  '/settings',
  '/activity',
])

export interface ScrollDecision {
  kind: NavKind
  /** Whether the destination is somewhere you return to. */
  tabRoot: boolean
  /** Whether this navigation stayed on the same path. */
  samePath: boolean
  /** Where this exact history entry was left. */
  savedForEntry: number | undefined
  /** Where this path was last left, whichever entry that was. */
  savedForPath: number | undefined
}

/**
 * The offset to scroll to, or null to leave the page where it is.
 *
 * The order of the branches is load-bearing, and one of them is not where you
 * would first put it. **React Router turns a link to the location you are
 * already on into a REPLACE**, not a PUSH — measured, not assumed — so tapping
 * the tab you are standing on arrives here looking exactly like a query string
 * being edited. Asking whether the path stayed the same has to come *before*
 * the REPLACE rule, or the one gesture every phone answers with "back to the
 * top" would answer with nothing at all.
 */
export function scrollTargetFor({
  kind,
  tabRoot,
  samePath,
  savedForEntry,
  savedForPath,
}: ScrollDecision): number | null {
  // Back and forward are about entries, not pages. Two visits to the same feed
  // are two entries with two positions, and stepping back through both should
  // return to each in turn.
  if (kind === 'POP') return savedForEntry ?? 0

  // Tapping the tab you are already on. Every phone treats this as "take me
  // back to the top", and in a feed that never ends it is the only quick way
  // to get there.
  if (samePath && tabRoot) return 0

  // Not a journey — the same screen restating its own address, which in this
  // app is a search query being edited or a redirect away from an unknown
  // path. Scrolling would yank the page out from under someone who only typed
  // a character.
  if (kind === 'REPLACE') return null

  if (tabRoot) return savedForPath ?? 0
  return 0
}

/**
 * Whether the page is tall enough to honour a restore yet.
 *
 * Returning to a feed means the grid has to be rebuilt before there is anywhere
 * to scroll to. The query cache makes that fast but not instant, and a restore
 * attempted too early silently lands at the bottom of a half-built page — which
 * looks exactly like the position having been forgotten.
 */
export function canReach(
  target: number,
  scrollHeight: number,
  viewportHeight: number,
): boolean {
  return scrollHeight - viewportHeight >= target
}
