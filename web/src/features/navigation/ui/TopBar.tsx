import { Bell, Menu } from 'lucide-react'
import {} from 'react-router-dom'
import { useIngestJobs } from '@/features/catalog/application/queries'
import { SearchBox } from './SearchBox'
import { IconButton } from '@/shared/ui/primitives'
import { AccountMenu } from '@/features/identity/ui/AccountMenu'
import { useTranslation } from 'react-i18next'
import { PageLink } from '@/shared/ui/PageLink'

/**
 * Top bar. Deviations from youtube.com follow the "no dead buttons" rule in
 * CLAUDE.md §5: there is no Create button, because content arrives from
 * topics.yaml rather than from anything a user types here. The bell reports
 * real download activity.
 */
/**
 * @param opacity 1 normally. Below that only while the phone's watch layer is
 * being dragged away: the bar belongs to the page underneath, so it arrives at
 * the same rate that page is revealed — off the same number as the bottom bar.
 *
 * It used to be omitted entirely on that screen and re-appear when the
 * navigation committed, which was two faults in one frame: the bar arrived
 * after the page it belongs to, and the scroller's top padding came with it, so
 * the content underneath jumped 56px at the moment the drag finished.
 */
export function TopBar({
  onToggleSidebar,
  opacity = 1,
}: {
  onToggleSidebar: () => void
  opacity?: number
}) {
  const { t } = useTranslation()

  // The badge reports real ingest activity rather than imaginary social
  // notifications: downloads in flight, and failures that need attention.
  const { data: jobs } = useIngestJobs(false)
  const active = (jobs ?? []).filter((j) => j.state === 'QUEUED' || j.state === 'RUNNING').length
  const failed = (jobs ?? []).filter((j) => j.state === 'FAILED').length
  const pendingIngest = active + failed

  return (
    // Pinned to the frame, not sticky to the flow.
    //
    // The page scrolls inside <main>, which fills the whole frame and carries
    // 56px of top padding for this bar — so the bar never moves and content
    // passes behind it. Sticky was what flickered: a sticky element is
    // repositioned every time the viewport resizes, which on a phone is every
    // time the browser's address bar slides in or out.
    //
    // Translucent over a blur, so what is behind reads as depth rather than as
    // a seam. A deliberate step away from the reference screenshots, where the
    // bar is opaque — design-system MASTER.md records it rather than being
    // quietly contradicted.
    //
    // `chrome-blur` carries the alpha and the blur, shared with the chip row
    // beneath and the player's bar on a phone — see index.css. One class
    // because the alpha is the figure that gets judged by eye, and as three
    // literals the surfaces drift apart.
    //
    // `absolute` with a z-index keeps the stacking context the search
    // suggestions depend on, which `sticky` used to provide.
    <header
      style={{
        // The background reaches the top edge of the screen; the content stays
        // in the 56px below the status bar. Without this the logo and the
        // search field sat under the clock on a notched phone — and the blurred
        // surface stopped short, leaving a flat band above itself.
        //
        // The same shape BottomNav already uses for the home indicator.
        height: 'var(--top-bar)',
        paddingTop: 'var(--safe-top)',
        opacity,
        // A bar at a third of its opacity is not yet a bar.
        pointerEvents: opacity < 1 ? 'none' : undefined,
      }}
      aria-hidden={opacity === 0 ? true : undefined}
      className="chrome-blur absolute inset-x-0 top-0 z-40 flex items-center
                 gap-5 px-4"
    >
      <div className="flex shrink-0 items-center gap-1 min-[700px]:min-w-[156px]">
        {/* Desktop only. There it does two jobs — collapsing the rail, and on the
            watch page opening the drawer, which is the only way off that page
            besides the logo because the rail is hidden there. Below the
            breakpoint the bottom bar carries navigation, so this is one more
            thing competing for a 390px-wide row. */}
        <span className="hidden min-[700px]:contents">
          <IconButton label={t('search.toggleSidebar')} onClick={onToggleSidebar}>
            <Menu size={24} />
          </IconButton>
        </span>
        <PageLink to="/" className="ml-1 flex items-center gap-1.5" aria-label={t('nav.home')}>
          <svg viewBox="0 0 28 20" width={30} height={22} aria-hidden>
            <path
              d="M27.4 3.1a3.5 3.5 0 0 0-2.5-2.5C22.7 0 14 0 14 0S5.3 0 3.1.6A3.5 3.5 0 0 0 .6 3.1C0 5.3 0 10 0 10s0 4.7.6 6.9a3.5 3.5 0 0 0 2.5 2.5c2.2.6 10.9.6 10.9.6s8.7 0 10.9-.6a3.5 3.5 0 0 0 2.5-2.5c.6-2.2.6-6.9.6-6.9s0-4.7-.6-6.9Z"
              fill="var(--color-brand)"
            />
            <path d="M11.2 14.3 18.4 10l-7.2-4.3v8.6Z" fill="#fff" />
          </svg>
          {/* The wordmark is the cheapest thing to cut when the row is short:
              the mark alone still says where home is. */}
          <span className="hidden text-xl font-medium tracking-tight min-[700px]:inline">
            MyTube
          </span>
        </PageLink>
      </div>

      <SearchBox />

      <div className="flex shrink-0 items-center gap-2 justify-end min-[700px]:min-w-[156px]">
        {/* The badge counts ingest events, and the Activity page is where those
            events are listed — so the bell goes there. It had the count but no
            destination, which made it the one control on this bar that reported
            something and then refused to explain it. */}
        <PageLink
          to="/activity"
          aria-label={`Downloads: ${active} active, ${failed} failed`}
          title={`Downloads: ${active} active, ${failed} failed`}
          className="relative hidden h-10 w-10 shrink-0 place-items-center rounded-full text-text transition-colors duration-150 ease-out hover:bg-surface-hover min-[700px]:grid"
        >
          <Bell size={22} />
          {pendingIngest > 0 && (
            <span className="absolute top-1 right-1 grid min-w-4 place-items-center rounded-full bg-brand px-1 text-[10px] leading-4 font-medium text-white">
              {pendingIngest > 9 ? '9+' : pendingIngest}
            </span>
          )}
        </PageLink>

        <AccountMenu />
      </div>
    </header>
  )
}
