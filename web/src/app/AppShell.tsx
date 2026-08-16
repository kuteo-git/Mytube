import clsx from 'clsx'
import { useEffect, useState } from 'react'
import { Routes, useLocation, useNavigate } from 'react-router-dom'
import { pageRoutes } from './routes'
import { useScrollRestoration } from '@/features/navigation/application/use-scroll-restoration'
import { bareTitle, isWatchScreen } from '@/features/navigation/application/bare-screens'
import { BackBar } from '@/features/catalog/ui/BackBar'
import { BottomNav } from '@/features/navigation/ui/BottomNav'
import { Sidebar } from '@/features/navigation/ui/Sidebar'
import { CookieExpiryBanner } from '@/features/settings/ui/CookieExpiryBanner'
import { TopBar } from '@/features/navigation/ui/TopBar'
import { Player } from '@/features/watch/ui/Player'
import { PlayerProvider, usePlayer } from '@/features/watch/application/player-context'
import { BOTTOM_NAV_HEIGHT } from '@/features/watch/application/player-geometry'
import { useResumeLastWatched } from '@/features/watch/application/use-resume'
import { dismissFade, layerOpacity } from '@/features/watch/application/watch-overlay'
import { ToastProvider } from '@/shared/ui/toast'

export function AppShell() {
  const { pathname } = useLocation()
  const isWatch = isWatchScreen(pathname)

  return (
    <PlayerProvider isWatch={isWatch}>
      <ToastProvider>
        <AppShellInner />
      </ToastProvider>
    </PlayerProvider>
  )
}

function AppShellInner() {
  const location = useLocation()
  const { pathname } = location
  const isWatch = isWatchScreen(pathname)
  const [expanded, setExpanded] = useState(true)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const {
    isMobile,
    mode,
    miniReserve,
    safeBottom,
    scrollerRef,
    scrollerEl,
    dragOffset,
    chromeHidden,
    background,
    canGoBack,
  } = usePlayer()


  const watchIsALayer = isWatch && isMobile

  // Whether the bars are drawn at all — read from the provider rather than
  // worked out again here. The player's own geometry depends on the same fact
  // (its bar sits above the navigation), and as two calculations they drifted:
  // the shell dropped the room for a navigation that was not there while the
  // player went on reserving it, leaving the mobile bar floating a tab bar's
  // height above the bottom of a channel page.
  //
  // A channel is not a *layer* like the watch screen: nothing underneath has to
  // stay alive, and the scroll position on the way back is already handled by
  // `/channel/*` not being a tab root, so a drill-in restores by entry.

  // Forward starts at the top, back returns you where you were — the manners a
  // phone taught everyone. A single-page router never reloads the document, so
  // without this the offset just stays where the previous page left it.
  //
  // Withheld while the watch layer is up, and that is not a detail. `<main>`
  // then holds the page *underneath*, while the location says `/watch` — so the
  // hook read "a new screen, start at the top" and scrolled somebody else's
  // page to zero. The drag then revealed a History that had been quietly
  // rewound, and the `navigate(-1)` at the end put the real position back,
  // which showed as the page jumping the instant the player reached the corner.
  // One cause, two complaints.
  //
  // Nothing needs saving in the meantime either: the layer above has its own
  // scroller, and the one underneath is not being touched.
  useScrollRestoration(watchIsALayer ? null : scrollerEl)

  // How far the drag has cleared the layer away. Everything but the player goes
  // within DISMISS_FADE_FRACTION of the screen's height; the picture keeps
  // travelling for the rest of it.
  const fade =
    watchIsALayer && dragOffset !== null ? dismissFade(dragOffset, window.innerHeight) : 0

  // Put back whatever this browser was in the middle of, in the corner, paused.
  const resuming = useResumeLastWatched(isWatch)

  // Room at the foot of the page for the miniplayer to sit over.
  //
  // Without it the last row of a grid is simply underneath the corner window,
  // and the only way to see it is to overscroll and hold — which stops being
  // possible at all once the rubber band is disabled. The number comes from the
  // geometry rather than being written out here, so it stays right as the
  // miniplayer sizes itself against the viewport.
  //
  // Also reserved while a resume is still on its way. The alternative is what
  // reopening the app used to do: draw the page, then have a corner window
  // arrive and push three hundred pixels of layout around underneath it. The
  // entry is read from storage synchronously, so that arrival is known about
  // before the first paint and there is no reason to be surprised by it.
  //
  // Not reserved otherwise. Always leaving the gap would put an unexplained
  // stretch of nothing at the bottom of every page.
  // The navigation's real height, which is more than the bar itself on a phone
  // with a home indicator. Reserving only the nominal 3.5rem left the last row
  // of every grid tucked under the labels.
  // No bar, no room for one. A screen that draws its own chrome — the watch
  // layer, a channel — would otherwise end in a band of nothing.
  const navReserve = isMobile && !chromeHidden ? BOTTOM_NAV_HEIGHT + safeBottom : 0
  const reservedBottom = mode === 'mini' || resuming ? miniReserve : navReserve || undefined

  // youtube.com hides the rail on the watch page to give the player room, and
  // reaches it through a drawer instead. The drawer overlays rather than pushes:
  // narrowing the picture mid-playback to make room for navigation is a worse
  // trade than covering it for the second the viewer is actually navigating.
  // Below the mobile breakpoint there is no rail at all — the bottom bar has it.
  const showFullSidebar = expanded && !isWatch && !isMobile
  const showMiniSidebar = !showFullSidebar && !isWatch && !isMobile

  // The rail's width slides when the viewer collapses it, and does not when the
  // route changes.
  //
  // Both used to slide, and that quietly broke the player's animation: leaving a
  // page with the rail showing hides it, so the content margin was still moving
  // while the player was flying towards the slot inside that content. The player
  // was chasing a target that had not stopped, which came out as two movements —
  // up first, then across — instead of the one diagonal it was asked for.
  //
  // A margin that animates only on the gesture that asks for it has nothing to
  // do with navigation, so the slot is already where it will end up by the time
  // anything is measured.
  const [slideMargin, setSlideMargin] = useState(false)

  // Leaving the watch page must not leave a drawer hanging over the grid.
  useEffect(() => {
    setDrawerOpen(false)
    setSlideMargin(false)
  }, [pathname])

  useEffect(() => {
    if (!drawerOpen) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setDrawerOpen(false)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [drawerOpen])

  return (
    // A frame that exactly fills the screen, with the scrolling confined to the
    // element inside it. The window itself never scrolls, and that is the
    // point: on a phone, scrolling the window slides the browser's own address
    // bar in and out, which resizes the viewport and moves everything pinned to
    // the top edge. Restoring a scroll position therefore flickered the top bar
    // on every tab switch. An element that scrolls inside a fixed frame is what
    // a native app does, and the browser chrome stays where it is.
    //
    // The bar overlays the scrolling region rather than sitting above it, which
    // is what gives it something to blur: content passes *behind* it. The room
    // it needs is `--top-bar` on the scroller — its own height plus the status
    // bar it bleeds up under, added in one place rather than in each of the
    // half-dozen things that begin beneath it.
    <div className="relative h-dvh overflow-hidden bg-bg">
      {/* No header on the phone's watch screen — it is a screen of its own
          rather than a page inside the app's chrome, and the way out is the
          drag with the browser's own back button behind it.
          
          Rendered rather than omitted, at the opacity the drag has reached, so
          it arrives with the page it belongs to instead of after it. */}
      {/* A screen you arrived at rather than passed through gets a back bar in
          place of the app's own. Drawn here so every such screen gets one
          without having to remember, except a channel: `bareTitle` returns null
          for it because ChannelHeader already names it in large type, and the
          bar there fades its own copy in only once that has scrolled away —
          behaviour the page owns. */}
      {chromeHidden && !isWatch && bareTitle(pathname) !== null && (
        <BackBar title={bareTitle(pathname) ?? ''} showTitle fallback="/" />
      )}

      {/* Above everything, and only when a session has actually ended: the
          household's subscriptions stop updating silently otherwise. */}
      <CookieExpiryBanner />

      <TopBar
        opacity={chromeHidden ? fade : 1}
        onToggleSidebar={() => {
          if (isWatch) {
            setDrawerOpen((o) => !o)
            return
          }
          setSlideMargin(true)
          setExpanded((e) => !e)
        }}
      />

      {showFullSidebar && <Sidebar mini={false} />}
      {showMiniSidebar && <Sidebar mini />}

      {isWatch && drawerOpen && !isMobile && (
        <>
          {/* Above the player, scrim included. Navigation that opens *behind*
              the thing it is meant to navigate away from is not navigation. */}
          <div
            className="fixed inset-0 top-[var(--top-bar)] z-40 bg-black/50"
            onClick={() => setDrawerOpen(false)}
            aria-hidden
          />
          <div className="fixed top-[var(--top-bar)] bottom-0 left-0 z-50">
            <Sidebar mini={false} />
          </div>
        </>
      )}

      <main
        ref={scrollerRef}
        // Which page is being held underneath, when one is. Written out because
        // it is the one thing about this arrangement that is decided rather
        // than derived, and a wrong answer here is invisible until somebody
        // drags the layer away and finds the wrong screen behind it.
        data-background={watchIsALayer ? background.pathname : undefined}
        className={clsx(
          // `relative` is what makes the host's `absolute` mean "within the
          // scrolled content" — the same thing it used to mean against the
          // document, now that the document is not what moves.
          // The overscroll rule moves here with the scrolling. It was on the
          // document, where CLAUDE.md §8b records why: a vertical bounce that
          // chains out is read as a gesture, and the gesture it was read as
          // threw away what you were watching.
          // The bar's room, reserved unconditionally.
          //
          // Not withheld while the watch layer is up: the page underneath is an
          // ordinary tab and expects the gap, and taking it away meant the
          // content sat 56px too high for the whole drag and then jumped down
          // the instant the navigation committed.
          'relative h-full overflow-y-auto overscroll-y-contain pt-[var(--top-bar)]',
          slideMargin && 'transition-[margin] duration-200 ease-out',
          showFullSidebar && 'ml-60',
          showMiniSidebar && 'ml-[72px]',
        )}
        style={reservedBottom === undefined ? undefined : { paddingBottom: reservedBottom }}
      >
        {/* The page underneath, and — when there is no layer over it — simply
            the page.

            Always a `<Routes>`, never an `<Outlet/>`, and that is the whole
            reason this reads oddly. React reconciles by element type at each
            position, so swapping one for the other when the watch screen opens
            tears the page down and builds a new one: same pixels, no state.
            Everything scrolled, typed, expanded or loaded on the tab was lost
            the moment a video was opened, which rather defeats keeping it.
            Same type in the same place, and only the location changes. */}
        <Routes location={watchIsALayer ? background : location}>{pageRoutes}</Routes>
        {/* Inside the scroller, because the full-size player is positioned
            within the content and has to travel with it. The miniplayer is
            `fixed`, which is measured from the viewport wherever it sits —
            `overflow` does not trap it, only a transformed ancestor would. */}
        <PlayerHost canGoBack={canGoBack} />
      </main>

      {/* The watch screen itself, over the page it was opened from.
          Below the player host on purpose: the picture belongs to neither
          layer, and travels across both. */}
      {watchIsALayer && (
        <div
          className="absolute inset-0 z-20 overflow-y-auto overscroll-y-contain bg-bg"
          style={{
            opacity: layerOpacity(fade),
            // Gone from the touch surface as soon as it starts to leave. A
            // half-faded page is not a page you can press things on, and the
            // finger is on its way to the tab underneath.
            pointerEvents: fade > 0 ? 'none' : undefined,
          }}
        >
          {/* The watch screen itself. Its own `<Routes>` for the same reason as
              above, rather than an Outlet that would resolve to the same thing
              by a different mechanism. */}
          <Routes location={location}>{pageRoutes}</Routes>
        </div>
      )}

      {/* Fades in exactly as the layer above it fades out — one number seen
          from two sides, so the navigation cannot arrive after the page it
          belongs to. */}
      <BottomNav opacity={chromeHidden ? fade : 1} />
    </div>
  )
}

/**
 * The single element the Player lives in, for the whole life of the app.
 *
 * It is never re-parented and the Player inside it is never remounted; only this
 * element's position and size change. That is what keeps the `<video>` — its
 * buffer, its position, its playback — intact across every transition.
 */
function PlayerHost({ canGoBack }: { canGoBack: boolean }) {
  const {
    state,
    mode,
    placement,
    deactivate,
    dismiss,
    restore,
    isMobile,
    isWatch,
    pauseToken,
    scrollerEl: scroller,
    setDragOffset,
    dragFraction,
    chromeHidden,
  } = usePlayer()
  const navigate = useNavigate()

  if (!state || !placement) return null

  const variant = mode === 'full' ? 'full' : isMobile ? 'bar' : 'mini'

  // Expanding is three things, and it is three things everywhere rather than
  // branching on where the viewer happens to be.
  //
  // Navigating alone was the whole bug: asking the router for the page it is
  // already on does nothing, so the button did nothing. Unpinning alone would be
  // worse — the player would return to a slot that is scrolled off screen and
  // carry on playing out of sight. And scrolling alone works only on desktop,
  // where an observer notices the slot and unpins; on mobile there is no
  // observer at all, so nothing would ever undo a pin the gesture made.
  const onExpand = () => {
    if (!isWatch) navigate(`/watch/${state.videoId}`)
    restore()
    // Desktop only, and that qualifier is the whole of it.
    //
    // There the full-size player lives in a slot on the watch page, so the page
    // has to be at the top for the slot to be on screen — and not smoothly,
    // because the bridging frame reads the scroller's offset to convert between
    // fixed and absolute, and a scroll still in motion would have it read a
    // number about to be wrong.
    //
    // On a phone there is no slot: the player is `fixed`, and the watch screen
    // is a layer over the tab rather than a page inside it. Scrolling here does
    // nothing for the player and throws away the position of the tab
    // underneath — which the next drag is about to reveal, at the top, having
    // apparently forgotten where it was.
    if (!isMobile) scroller?.scrollTo({ top: 0 })
  }

  const onClose = () => (isWatch ? dismiss() : deactivate())

  return (
    <div
      data-testid="player-host"
      // Marks the player as not part of the page's scroll surface. It lives
      // inside <main> so its `absolute` placement travels with the content —
      // which also puts it within reach of anything listening for touches
      // there, and pull-to-refresh was one: dragging the picture down to put it
      // away pulled the page underneath at the same time.
      data-player-host=""
      // Below the navigation chrome, above the page. The miniplayer is content
      // that outstayed its page, not a layer over the app.
      className={clsx(
        'z-30 overflow-hidden',
        // Black behind the picture everywhere the picture fills the frame.
        //
        // The mobile bar is the exception worth making: there the video is only
        // 128px wide (Player.tsx sizes it `w-32` in that variant), so the title
        // and the controls beside it sit on the host's own background rather
        // than on any part of the film. That strip is the one place a blur has
        // something to work on, and it is the strip that reads as chrome.
        //
        // Not applied to the desktop miniplayer: there the video fills the
        // frame, so a blurred backdrop would be covered by it entirely and
        // would only cost a compositing layer.
        variant === 'bar' ? 'chrome-blur' : 'bg-black',
        // A corner-resident player is a floating object and should read as one.
        // shadow-2xl alone did not: over a dark page a black shadow is nearly
        // invisible, so the ring is what actually draws the edge.
        mode === 'mini' && 'shadow-2xl shadow-black/60 ring-1 ring-white/10',
      )}
      style={{
        position: placement.position,
        top: placement.rect.top,
        left: placement.rect.left,
        width: placement.rect.width,
        height: placement.rect.height,
        // Rounded whenever it is a card on a page: full-screen on a phone and
        // the docked player are edge-to-edge, and everything else matches the
        // 12px the thumbnails and cards around it use.
        borderRadius: isMobile && mode === 'full'
          ? 0
          : isMobile && mode === 'mini'
            ? '24px 24px 0 0'
            : 12,
        // The bar reaches the bottom edge on a screen with no navigation, so
        // its surface runs under the home indicator the way every other bar in
        // the app does. Its *content* stays above it: the padding is inside the
        // host, and the player fills what is left.
        paddingBottom:
          variant === 'bar' && chromeHidden ? 'var(--safe-bottom)' : undefined,
        transition: placement.animate
          ? 'top 300ms cubic-bezier(0.4, 0, 0.2, 1), left 300ms cubic-bezier(0.4, 0, 0.2, 1),' +
            ' width 300ms cubic-bezier(0.4, 0, 0.2, 1), height 300ms cubic-bezier(0.4, 0, 0.2, 1)'
          : 'none',
      }}
    >
      <Player
        key={state.videoId}
        videoId={state.videoId}
        title={state.title}
        channelTitle={state.channelTitle}
        hue={state.hue}
        durationSeconds={state.durationSeconds}
        initialPositionSeconds={state.initialPositionSeconds}
        mediaState={state.mediaState}
        subtitles={state.subtitles}
        thumbnailURL={state.thumbnailURL}
        nextVideoTitle={state.nextVideoTitle}
        onPlayNext={state.onPlayNext}
        variant={variant}
        // Only from full size on a phone. Dragging is a request to go back to
        // browsing, and leaving the watch page is exactly what turns the player
        // into the bar — so the gesture needs no minimised state of its own.
        onSwipeDown={
          variant === 'full' && isMobile
            ? () => {
                // Back, not Home. The layer underneath is the page this was
                // opened from — History if you came from History — and popping
                // the entry is what puts the layer away while returning that
                // page to the scroll position it was left at.
                //
                // `canGoBack` is the same fact that decides what is drawn
                // underneath: a page was remembered, so we arrived from it.
                // Deriving it from anything else — `window.history.state.idx`
                // was tried — risks the two disagreeing, which would show as a
                // drag revealing one page and landing on another. Without it,
                // popping walks out of the app entirely, which is what a shared
                // LAN link opens onto.
                if (canGoBack) navigate(-1)
                else navigate('/', { replace: true })
                // Released on the next frame, once the navigation has been
                // committed and the placement is the bar's own. At full
                // progress the drag rectangle already *is* the corner, so
                // handing over changes nothing on screen.
                requestAnimationFrame(() => setDragOffset(null))
              }
            : undefined
        }
        onSwipeProgress={setDragOffset}
        morph={dragFraction}
        onClose={onClose}
        onExpand={onExpand}
        pauseToken={pauseToken}
        autoplay={state.autoplay !== false}
      />
    </div>
  )
}
