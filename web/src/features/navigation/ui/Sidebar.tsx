import clsx from 'clsx'
import {
  Clapperboard,
  Compass,
  HardDrive,
  History,
  Home,
  ListVideo,
  ThumbsUp,
} from 'lucide-react'
import type { ComponentType } from 'react'
import { NavLink } from 'react-router-dom'
import { useCategories } from '@/features/catalog/application/queries'

type Icon = ComponentType<{ size?: number }>

interface Item {
  icon: Icon
  label: string
  to: string
}

/**
 * Compared to youtube.com we drop "Your videos", "YouTube Music", "YouTube
 * Kids", Shorts, Live and the legal footer, and repurpose "Downloads" into
 * "Storage" — see CLAUDE.md §5, "no dead buttons".
 */
const PRIMARY: Item[] = [
  { icon: Home, label: 'Home', to: '/' },
  { icon: History, label: 'History', to: '/history' },
  { icon: ListVideo, label: 'Playlists', to: '/playlists' },
  { icon: Clapperboard, label: 'Watch later', to: '/watch-later' },
  { icon: ThumbsUp, label: 'Liked videos', to: '/liked' },
  { icon: HardDrive, label: 'Storage', to: '/storage' },
]

function Row({ item, mini }: { item: Item; mini: boolean }) {
  const { icon: Icon, label, to } = item
  return (
    <NavLink
      to={to}
      end={to === '/'}
      className={({ isActive }) =>
        clsx(
          'flex items-center rounded-[10px] transition-colors duration-150 ease-out hover:bg-surface-hover',
          mini ? 'h-[74px] flex-col justify-center gap-1.5 px-1' : 'h-10 gap-6 px-3',
          isActive && 'bg-surface-hover font-medium',
        )
      }
    >
      <Icon size={24} />
      <span className={mini ? 'text-[10px] leading-tight' : 'text-sm whitespace-nowrap'}>
        {label}
      </span>
    </NavLink>
  )
}

export function Sidebar({ mini }: { mini: boolean }) {
  // Explore entries are the categories actually present in the library, so the
  // section can never offer a filter that yields nothing.
  const { data: categories } = useCategories()

  if (mini) {
    return (
      <nav
        aria-label="Main"
        className="fixed top-14 bottom-0 left-0 z-20 w-[72px] overflow-y-auto bg-bg py-1 no-scrollbar"
      >
        {PRIMARY.slice(0, 5).map((item) => (
          <Row key={item.to} item={item} mini />
        ))}
      </nav>
    )
  }

  return (
    <nav
      aria-label="Main"
      className="fixed top-14 bottom-0 left-0 z-20 w-60 overflow-y-auto bg-bg px-3 py-3 no-scrollbar"
    >
      <section className="flex flex-col gap-0.5">
        {PRIMARY.map((item) => (
          <Row key={item.to} item={item} mini={false} />
        ))}
      </section>

      {categories && categories.length > 0 && (
        <>
          <hr className="my-3 border-0 border-t border-line" />
          <section className="flex flex-col gap-0.5">
            <h2 className="px-3 py-1.5 text-base font-medium">Explore</h2>
            {categories.slice(0, 6).map((category) => (
              <NavLink
                key={category.name}
                to={`/tag/${encodeURIComponent(category.name)}`}
                className="flex h-10 items-center gap-6 rounded-[10px] px-3 transition-colors duration-150 ease-out hover:bg-surface-hover"
              >
                <Compass size={24} />
                <span className="clamp-1 text-sm">{category.name}</span>
                <span className="ml-auto text-xs text-text-2">{category.videoCount}</span>
              </NavLink>
            ))}
          </section>
        </>
      )}
    </nav>
  )
}
