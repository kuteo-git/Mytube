import clsx from 'clsx'
import { Activity, Bookmark, Clock, HardDrive, Home } from 'lucide-react'
import type { ComponentType } from 'react'
import { NavLink } from 'react-router-dom'

/**
 * The mobile shell's navigation, replacing the sidebar rail below the breakpoint.
 *
 * The five entries are the same five the sidebar shows, and every one of them
 * has a real route. youtube's own bar carries Shorts, Subscriptions and You,
 * none of which exist here — putting them in would be exactly the dead button
 * the charter forbids, so the bar reflects this app rather than that one.
 */
const ITEMS: { icon: ComponentType<{ size?: number }>; label: string; to: string }[] = [
  { icon: Home, label: 'Home', to: '/' },
  { icon: Bookmark, label: 'Saved', to: '/saved' },
  { icon: Clock, label: 'History', to: '/history' },
  { icon: HardDrive, label: 'Storage', to: '/storage' },
  { icon: Activity, label: 'Activity', to: '/activity' },
]

export function BottomNav() {
  return (
    <nav
      className="fixed inset-x-0 bottom-0 z-50 flex h-14 items-stretch border-t border-white/10
                 bg-bg min-[700px]:hidden"
      // The bar keeps its own height and grows underneath it, so the labels stay
      // clear of the home indicator instead of sitting under it.
      style={{ paddingBottom: 'var(--safe-bottom)', height: 'calc(3.5rem + var(--safe-bottom))' }}
      aria-label="Main"
    >
      {ITEMS.map(({ icon: Icon, label, to }) => (
        <NavLink
          key={to}
          to={to}
          end={to === '/'}
          className={({ isActive }) =>
            clsx(
              'flex flex-1 flex-col items-center justify-center gap-1 text-[10px]',
              isActive ? 'text-text-1' : 'text-text-2',
            )
          }
        >
          <Icon size={20} />
          <span>{label}</span>
        </NavLink>
      ))}
    </nav>
  )
}
