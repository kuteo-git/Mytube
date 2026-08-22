import clsx from 'clsx'
import { Clock, Home, Settings, Users } from 'lucide-react'
import type { ComponentType } from 'react'
import {} from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import type { TranslationKey } from '@/shared/i18n/en'
import { TabLink } from '@/shared/ui/PageLink'

/**
 * The mobile shell's navigation, replacing the sidebar rail below the breakpoint.
 *
 * Five entries, every one with a real route. youtube's own bar carries Shorts,
 * Subscriptions and You, none of which exist here — putting them in would be
 * exactly the dead button the charter forbids, so the bar reflects this app
 * rather than that one.
 *
 * Five is the ceiling rather than a coincidence: past that the targets fall
 * below the 44px a finger needs. What is here is what the bar is *for* — the
 * places you move between while browsing — and everything else was moved to the
 * top of Settings rather than being stranded: Storage, Activity, and now Saved.
 *
 * Saved is the one worth justifying, since it is content rather than a
 * diagnostic. It is a list you go to when you have decided to watch something
 * you kept, which is a deliberate act like opening Settings — not a place you
 * pass through on the way to somewhere else, the way Home and Subscriptions
 * and History are.
 *
 * Subscriptions earns its place because a phone has no sidebar, and without it
 * the only route to a channel was to find one of its videos and tap through —
 * a poor way to answer "what has this channel posted", which is the question
 * following a channel is for.
 */
const ITEMS: { icon: ComponentType<{ size?: number }>; label: TranslationKey; to: string }[] = [
  { icon: Home, label: 'nav.home', to: '/' },
  { icon: Users, label: 'nav.subscriptions', to: '/subscriptions' },
  { icon: Clock, label: 'nav.history', to: '/history' },
  { icon: Settings, label: 'nav.settings', to: '/settings' },
]

/**
 * @param opacity 1 normally. Below that only while the watch layer is being
 * dragged away: the bar belongs to the page underneath, so it arrives at the
 * same rate that page is revealed. Hidden from assistive technology and from
 * the pointer while it is on its way in, because a control at a third of its
 * opacity is not yet a control.
 */
export function BottomNav({ opacity = 1 }: { opacity?: number }) {
  const { t } = useTranslation()
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
      aria-label={t('ui.main')}
    >
      {ITEMS.map(({ icon: Icon, label, to }) => (
        <TabLink
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
          <span>{t(label)}</span>
        </TabLink>
      ))}
    </nav>
  )
}
