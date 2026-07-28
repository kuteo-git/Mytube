import clsx from 'clsx'
import { Activity, Bookmark, HardDrive, History, Home, Tag } from 'lucide-react'
import type { ComponentType } from 'react'
import { NavLink } from 'react-router-dom'
import { useTopics } from '@/features/catalog/application/queries'

type Icon = ComponentType<{ size?: number }>

interface Item {
  icon: Icon
  label: string
  to: string
}

/**
 * Fixed entries plus the topics from topics.yaml.
 *
 * Compared to youtube.com this drops Playlists, Watch later, Liked videos,
 * Your videos, Subscriptions, YouTube Music, YouTube Kids, Shorts, Live and the
 * legal footer. None of them had anything behind them: content is organised by
 * topic, and the only personal collection is what you Keep — see CLAUDE.md §5.
 */
const PRIMARY: Item[] = [
  { icon: Home, label: 'Home', to: '/' },
  { icon: Activity, label: 'Activity', to: '/activity' },
  { icon: History, label: 'History', to: '/history' },
  { icon: Bookmark, label: 'Saved', to: '/saved' },
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
  const { data: topics } = useTopics()

  if (mini) {
    return (
      <nav
        aria-label="Main"
        className="fixed top-14 bottom-0 left-0 z-20 w-[72px] overflow-y-auto bg-bg py-1 no-scrollbar"
      >
        {PRIMARY.map((item) => (
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

      {topics && topics.length > 0 && (
        <>
          <hr className="my-3 border-0 border-t border-line" />
          <section className="flex flex-col gap-0.5">
            <h2 className="px-3 py-1.5 text-base font-medium">Topics</h2>
            {topics.map((topic) => (
              <NavLink
                key={topic.name}
                to={`/topic/${encodeURIComponent(topic.name)}`}
                className={({ isActive }) =>
                  clsx(
                    'flex h-10 items-center gap-6 rounded-[10px] px-3 transition-colors duration-150 ease-out hover:bg-surface-hover',
                    isActive && 'bg-surface-hover font-medium',
                  )
                }
              >
                <Tag size={24} />
                <span className="clamp-1 text-sm">{topic.name}</span>
                <span className="ml-auto text-xs text-text-2">{topic.videoCount}</span>
              </NavLink>
            ))}
          </section>
        </>
      )}
    </nav>
  )
}
