import clsx from 'clsx'
import { useEffect, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { useScrollRestoration } from '@/features/navigation/application/use-scroll-restoration'
import { BottomNav } from '@/features/navigation/ui/BottomNav'
import { Sidebar } from '@/features/navigation/ui/Sidebar'
import { TopBar } from '@/features/navigation/ui/TopBar'
import { Player } from '@/features/watch/ui/Player'
import { PlayerProvider, usePlayer } from '@/features/watch/application/player-context'
import { BOTTOM_NAV_HEIGHT } from '@/features/watch/application/player-geometry'
import { useResumeLastWatched } from '@/features/watch/application/use-resume'

export function AppShell() {
  const { pathname } = useLocation()
  const isWatch = pathname.startsWith('/watch')

  return (
    <PlayerProvider isWatch={isWatch}>
      <AppShellInner />
    </PlayerProvider>
  )
}

function AppShellInner() {
  const { pathname } = useLocation()
  const isWatch = pathname.startsWith('/watch')
  const [expanded, setExpanded] = useState(true)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const { isMobile, mode, miniReserve, safeBottom, scrollerRef, scrollerEl } =
    usePlayer()

  // Forward starts at the top, back returns you where you were — the manners a
  // phone taught everyone. A single-page router never reloads the document, so
  // without this the offset just stays where the previous page left it.
  useScrollRestoration(scrollerEl)

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
  const navReserve = isMobile ? BOTTOM_NAV_HEIGHT + safeBottom : 0
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
    // it needs is `pt-14` on the scroller — the same 56px it used to occupy as
    // a sibling, so nothing else moves.
    <div className="relative h-dvh overflow-hidden bg-bg">
      <TopBar
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
            className="fixed inset-0 top-14 z-40 bg-black/50"
            onClick={() => setDrawerOpen(false)}
            aria-hidden
          />
          <div className="fixed top-14 bottom-0 left-0 z-50">
            <Sidebar mini={false} />
          </div>
        </>
      )}

      <main
        ref={scrollerRef}
        className={clsx(
          // `relative` is what makes the host's `absolute` mean "within the
          // scrolled content" — the same thing it used to mean against the
          // document, now that the document is not what moves.
          // The overscroll rule moves here with the scrolling. It was on the
          // document, where CLAUDE.md §8b records why: a vertical bounce that
          // chains out is read as a gesture, and the gesture it was read as
          // threw away what you were watching.
          'relative h-full overflow-y-auto overscroll-y-contain pt-14',
          slideMargin && 'transition-[margin] duration-200 ease-out',
          showFullSidebar && 'ml-60',
          showMiniSidebar && 'ml-[72px]',
        )}
        style={reservedBottom === undefined ? undefined : { paddingBottom: reservedBottom }}
      >
        <Outlet />
        {/* Inside the scroller, because the full-size player is positioned
            within the content and has to travel with it. The miniplayer is
            `fixed`, which is measured from the viewport wherever it sits —
            `overflow` does not trap it, only a transformed ancestor would. */}
        <PlayerHost />
      </main>

      <BottomNav />
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
function PlayerHost() {
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
    // Not smooth, on purpose. The bridging frame reads the scroller's offset to
    // convert between fixed and absolute, and a smooth scroll would have it
    // read a number still in motion — the player would jump, then animate.
    // Scrolling synchronously means the offset is already 0 by the time that
    // runs, leaving a single movement: the corner to the frame.
    scroller?.scrollTo({ top: 0 })
  }

  const onClose = () => (isWatch ? dismiss() : deactivate())

  return (
    <div
      data-testid="player-host"
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
        variant === 'bar'
          ? 'bg-bg/70 backdrop-blur-xl backdrop-saturate-150'
          : 'bg-black',
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
        borderRadius: isMobile && mode === 'full' ? 0 : 12,
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
                navigate('/')
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
