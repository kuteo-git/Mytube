import clsx from 'clsx'
import { useEffect, useState } from 'react'
import { Outlet, useLocation, useNavigate } from 'react-router-dom'
import { BottomNav } from '@/features/navigation/ui/BottomNav'
import { Sidebar } from '@/features/navigation/ui/Sidebar'
import { TopBar } from '@/features/navigation/ui/TopBar'
import { Player } from '@/features/watch/ui/Player'
import { PlayerProvider, usePlayer } from '@/features/watch/application/player-context'
import { BOTTOM_NAV_HEIGHT, MINI_MARGIN } from '@/features/watch/application/player-geometry'

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
  const { isMobile, mode, placement } = usePlayer()

  // Room at the foot of the page for the miniplayer to sit over.
  //
  // Without it the last row of a grid is simply underneath the corner window,
  // and the only way to see it is to overscroll and hold — which stops being
  // possible at all once the rubber band is disabled. Taken from the rect the
  // player is actually using rather than written out here: the miniplayer sizes
  // itself against the viewport now, and a number typed in this file would be
  // wrong on most screens and would quietly stay wrong.
  //
  // Only while the miniplayer is showing. Reserving it always would leave a
  // gap at the bottom of every page that nothing on screen explains.
  const reservedBottom =
    mode === 'mini' && placement
      ? placement.rect.height + MINI_MARGIN * 2 + (isMobile ? BOTTOM_NAV_HEIGHT : 0)
      : undefined

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
    <div className="min-h-dvh bg-bg">
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
        className={clsx(
          slideMargin && 'transition-[margin] duration-200 ease-out',
          showFullSidebar && 'ml-60',
          showMiniSidebar && 'ml-[72px]',
          // Room for the bottom bar, so the last row is never sitting under it.
          isMobile && 'pb-14',
        )}
        style={reservedBottom === undefined ? undefined : { paddingBottom: reservedBottom }}
      >
        <Outlet />
      </main>

      <PlayerHost />
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
    onDrag,
    onDragEnd,
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
    // Not smooth, on purpose. The bridging frame reads window.scrollY to convert
    // between fixed and absolute, and a smooth scroll would have it read a
    // number still in motion — the player would jump, then animate. Scrolling
    // synchronously means scrollY is already 0 by the time that runs, leaving a
    // single movement: the corner to the frame.
    window.scrollTo({ top: 0 })
  }

  const onClose = () => (isWatch ? dismiss() : deactivate())

  return (
    <div
      data-testid="player-host"
      // Below the navigation chrome, above the page. The miniplayer is content
      // that outstayed its page, not a layer over the app.
      className={clsx('z-30 overflow-hidden bg-black', mode === 'mini' && 'shadow-2xl')}
      style={{
        position: placement.position,
        top: placement.rect.top,
        left: placement.rect.left,
        width: placement.rect.width,
        height: placement.rect.height,
        borderRadius: mode === 'full' && !isMobile ? 12 : 0,
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
        onClose={onClose}
        onExpand={onExpand}
        pauseToken={pauseToken}
        dragEnabled={isMobile && mode === 'full'}
        onDragMove={onDrag}
        onDragRelease={onDragEnd}
      />
    </div>
  )
}
