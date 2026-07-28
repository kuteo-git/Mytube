import { Bell, Menu, Mic, Plus, Search } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { AddVideoDialog } from '@/features/catalog/ui/AddVideoDialog'
import { useIngestJobs } from '@/features/catalog/application/queries'
import { Avatar, IconButton } from '@/shared/ui/primitives'

/**
 * Top bar. Note the deliberate deviations from youtube.com, per CLAUDE.md §5
 * ("no dead buttons"): the Create button becomes "Add video" (the ingest entry
 * point) and the notification bell reports ingest events.
 */
export function TopBar({ onToggleSidebar }: { onToggleSidebar: () => void }) {
  const [query, setQuery] = useState('')
  const [addOpen, setAddOpen] = useState(false)
  const navigate = useNavigate()

  // The badge reports real ingest activity rather than imaginary social
  // notifications: downloads in flight, and failures that need attention.
  const { data: jobs } = useIngestJobs(false)
  const active = (jobs ?? []).filter((j) => j.state === 'QUEUED' || j.state === 'RUNNING').length
  const failed = (jobs ?? []).filter((j) => j.state === 'FAILED').length
  const pendingIngest = active + failed

  return (
    <header className="sticky top-0 z-30 flex h-14 items-center gap-2 bg-bg px-4">
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

      <form
        className="mx-auto flex max-w-[720px] flex-1 items-center gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          navigate(`/results?q=${encodeURIComponent(query)}`)
        }}
      >
        <div className="flex h-10 flex-1 items-center rounded-full border border-line bg-surface-input focus-within:border-ring">
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search"
            aria-label="Search the library"
            className="h-full min-w-0 flex-1 rounded-l-full bg-transparent px-4 text-base outline-none placeholder:text-text-2"
          />
          <button
            type="submit"
            aria-label="Search"
            className="grid h-full w-16 place-items-center rounded-r-full border-l border-line bg-surface transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            <Search size={20} />
          </button>
        </div>
        <IconButton label="Search by voice" className="bg-surface hover:bg-surface-hover">
          <Mic size={20} />
        </IconButton>
      </form>

      <div className="flex shrink-0 items-center gap-2">
        <button
          type="button"
          onClick={() => setAddOpen(true)}
          className="flex h-9 items-center gap-2 rounded-full bg-surface px-4 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
        >
          <Plus size={20} />
          Add video
        </button>

        <IconButton
          label={`Downloads: ${active} active, ${failed} failed`}
          onClick={() => setAddOpen(true)}
          className="relative"
        >
          <Bell size={22} />
          {pendingIngest > 0 && (
            <span className="absolute top-1 right-1 grid min-w-4 place-items-center rounded-full bg-brand px-1 text-[10px] leading-4 font-medium text-white">
              {pendingIngest > 9 ? '9+' : pendingIngest}
            </span>
          )}
        </IconButton>

        <button type="button" aria-label="Account" className="ml-1 rounded-full">
          <Avatar hue={210} name="Luc" size={32} />
        </button>
      </div>
      {addOpen && <AddVideoDialog onClose={() => setAddOpen(false)} />}
    </header>
  )
}
