import clsx from 'clsx'
import { Activity, Bookmark, Clock, HardDrive, Home, Settings, Tag } from 'lucide-react'
import type { ComponentType } from 'react'
import { NavLink } from 'react-router-dom'
import { useSubscriptions, useTopics } from '@/features/catalog/application/queries'
import { Avatar } from '@/shared/ui/primitives'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'

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
  { icon: Bookmark, label: 'Saved', to: '/saved' },
  { icon: Clock, label: 'History', to: '/history' },
  { icon: HardDrive, label: 'Storage', to: '/storage' },
  { icon: Settings, label: 'Settings', to: '/settings' },
  { icon: Activity, label: 'Activity', to: '/activity' },
]

/**
 * Where the sidebar can take you.
 *
 * Exported so the mobile bar can be checked against it. The two carry different
 * subsets — five fit across a phone, six down a rail — but a path in one that
 * exists in neither the router nor the other is a link to the not-found page,
 * and nothing about rendering it would say so.
 */
export const SIDEBAR_ROUTES = PRIMARY.map((i) => i.to)

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
  const { data: subscriptions } = useSubscriptions()

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

      {subscriptions && subscriptions.length > 0 && (
        <>
          <hr className="my-3 border-0 border-t border-line" />
          <section className="flex flex-col gap-0.5">
            <h2 className="px-3 py-1.5 text-base font-medium">Subscriptions</h2>
            {subscriptions.map((channel) => (
              <NavLink
                key={channel.id}
                to={`/channel/${channel.id}`}
                className={({ isActive }) =>
                  clsx(
                    'flex h-10 items-center gap-6 rounded-[10px] px-3 transition-colors duration-150 ease-out hover:bg-surface-hover',
                    isActive && 'bg-surface-hover font-medium',
                  )
                }
              >
                <Avatar
                  hue={hueFromId(channel.id)}
                  name={channel.name}
                  src={mediaURL(channel.avatarPath)}
                  size={24}
                />
                <span className="clamp-1 text-sm">{channel.name}</span>
              </NavLink>
            ))}
          </section>
        </>
      )}

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
