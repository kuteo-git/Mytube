import clsx from 'clsx'
import { Activity, Bookmark, Clock, Home, Settings } from 'lucide-react'
import type { ComponentType } from 'react'
import { NavLink } from 'react-router-dom'

/**
 * The mobile shell's navigation, replacing the sidebar rail below the breakpoint.
 *
 * Five entries, every one with a real route. youtube's own bar carries Shorts,
 * Subscriptions and You, none of which exist here — putting them in would be
 * exactly the dead button the charter forbids, so the bar reflects this app
 * rather than that one.
 *
 * Five is also the ceiling rather than a coincidence: past that the targets fall
 * below the 44px a finger needs. So the sidebar's six entries have to lose one
 * here, and **Storage is the one that goes**. It is a page you visit when the
 * disk is filling up — occasionally, deliberately, and it announces itself
 * through the storage banner when it matters. Settings is the opposite: the
 * narration voice, its levels and the feed mix all live there, and on a phone it
 * had no way in at all. Storage is still reachable on a phone through that
 * banner's "Manage storage" link, so nothing is stranded.
 */
const ITEMS: { icon: ComponentType<{ size?: number }>; label: string; to: string }[] = [
  { icon: Home, label: 'Home', to: '/' },
  { icon: Bookmark, label: 'Saved', to: '/saved' },
  { icon: Clock, label: 'History', to: '/history' },
  { icon: Activity, label: 'Activity', to: '/activity' },
  { icon: Settings, label: 'Settings', to: '/settings' },
]

/**
 * @param opacity 1 normally. Below that only while the watch layer is being
 * dragged away: the bar belongs to the page underneath, so it arrives at the
 * same rate that page is revealed. Hidden from assistive technology and from
 * the pointer while it is on its way in, because a control at a third of its
 * opacity is not yet a control.
 */
export function BottomNav({ opacity = 1 }: { opacity?: number }) {
  const arriving = opacity < 1
  return (
    <nav
      className="fixed inset-x-0 bottom-0 z-50 flex h-14 items-stretch border-t border-white/10
                 bg-bg min-[700px]:hidden"
      // The bar keeps its own height and grows underneath it, so the labels stay
      // clear of the home indicator instead of sitting under it.
      style={{
        paddingBottom: 'var(--safe-bottom)',
        height: 'calc(3.5rem + var(--safe-bottom))',
        opacity,
        pointerEvents: arriving ? 'none' : undefined,
      }}
      aria-hidden={opacity === 0 ? true : undefined}
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
