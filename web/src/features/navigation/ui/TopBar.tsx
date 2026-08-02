import { Bell, Menu } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useIngestJobs } from '@/features/catalog/application/queries'
import { SearchBox } from './SearchBox'
import { Avatar, IconButton } from '@/shared/ui/primitives'

/**
 * Top bar. Deviations from youtube.com follow the "no dead buttons" rule in
 * CLAUDE.md §5: there is no Create button, because content arrives from
 * topics.yaml rather than from anything a user types here. The bell reports
 * real download activity.
 */
export function TopBar({ onToggleSidebar }: { onToggleSidebar: () => void }) {

  // The badge reports real ingest activity rather than imaginary social
  // notifications: downloads in flight, and failures that need attention.
  const { data: jobs } = useIngestJobs(false)
  const active = (jobs ?? []).filter((j) => j.state === 'QUEUED' || j.state === 'RUNNING').length
  const failed = (jobs ?? []).filter((j) => j.state === 'FAILED').length
  const pendingIngest = active + failed

  return (
    // Above the player host, which the search suggestions depend on: `sticky`
    // with a z-index makes this a stacking context, so anything inside it is
    // capped at whatever this header is worth.
    <header className="sticky top-0 z-40 flex h-14 items-center gap-2 bg-bg px-4">
      <div className="flex shrink-0 items-center gap-1">
        <IconButton label="Toggle sidebar" onClick={onToggleSidebar}>
          <Menu size={24} />
        </IconButton>
        <Link to="/" className="ml-1 flex items-center gap-1.5" aria-label="Home">
          <svg viewBox="0 0 28 20" width={30} height={22} aria-hidden>
            <path
              d="M27.4 3.1a3.5 3.5 0 0 0-2.5-2.5C22.7 0 14 0 14 0S5.3 0 3.1.6A3.5 3.5 0 0 0 .6 3.1C0 5.3 0 10 0 10s0 4.7.6 6.9a3.5 3.5 0 0 0 2.5 2.5c2.2.6 10.9.6 10.9.6s8.7 0 10.9-.6a3.5 3.5 0 0 0 2.5-2.5c.6-2.2.6-6.9.6-6.9s0-4.7-.6-6.9Z"
              fill="var(--color-brand)"
            />
            <path d="M11.2 14.3 18.4 10l-7.2-4.3v8.6Z" fill="#fff" />
          </svg>
          <span className="text-xl font-medium tracking-tight">Library</span>
        </Link>
      </div>

      <SearchBox />

      <div className="flex shrink-0 items-center gap-2">
        {/* The badge counts ingest events, and the Activity page is where those
            events are listed — so the bell goes there. It had the count but no
            destination, which made it the one control on this bar that reported
            something and then refused to explain it. */}
        <Link
          to="/activity"
          aria-label={`Downloads: ${active} active, ${failed} failed`}
          title={`Downloads: ${active} active, ${failed} failed`}
          className="relative grid h-10 w-10 shrink-0 place-items-center rounded-full text-text transition-colors duration-150 ease-out hover:bg-surface-hover"
        >
          <Bell size={22} />
          {pendingIngest > 0 && (
            <span className="absolute top-1 right-1 grid min-w-4 place-items-center rounded-full bg-brand px-1 text-[10px] leading-4 font-medium text-white">
              {pendingIngest > 9 ? '9+' : pendingIngest}
            </span>
          )}
        </Link>

        <button type="button" aria-label="Account" className="ml-1 rounded-full">
          <Avatar hue={210} name="Luc" size={32} />
        </button>
      </div>
    </header>
  )
}
