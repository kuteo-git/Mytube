import clsx from 'clsx'
import { useTranslation } from 'react-i18next'

import type { Dictionary } from '@/shared/i18n/en'
import {
  Activity,
  Bookmark,
  Clock,
  HardDrive,
  History,
  Home,
  KeyRound,
  ListVideo,
  Settings,
  Tag,
} from 'lucide-react'
import type { ComponentType } from 'react'
import {} from 'react-router-dom'
import { useSubscriptions, useTopics } from '@/features/catalog/application/queries'
import { Avatar } from '@/shared/ui/primitives'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'
import { useCurrentProfile, useProfiles } from '@/features/identity/application/use-profile'
import { PageNavLink } from '@/shared/ui/PageLink'

type Icon = ComponentType<{ size?: number }>

interface Item {
  icon: Icon
  /**
   * A translation key, not a word — see Row for why.
   *
   * Typed as the key rather than as `string`, which is what caught a typo the
   * first time this was compiled: `string` is wide enough to hold "nav.acount"
   * and t() would have rendered the key itself on screen, in both languages,
   * reported by nothing.
   */
  label: `nav.${keyof Dictionary['nav']}`
  to: string
}

/**
 * Fixed entries plus the topics from topics.yaml.
 *
 * Compared to youtube.com this drops Liked videos, Your videos, YouTube Music,
 * YouTube Kids, Shorts, Live and the legal footer. None of them has anything
 * behind it — see CLAUDE.md §5.
 *
 * Watch later was in that list until it stopped being empty. The table had
 * existed since 0001_init and every video already reported in_watch_later, but
 * nothing could write it, so the entry would have led to a page that was blank
 * for everybody.
 */
const PRIMARY: Item[] = [
  { icon: Home, label: 'nav.home', to: '/' },
  { icon: Bookmark, label: 'nav.saved', to: '/saved' },
  { icon: History, label: 'nav.history', to: '/history' },
  { icon: HardDrive, label: 'nav.storage', to: '/storage' },
  { icon: Settings, label: 'nav.settings', to: '/settings' },
  { icon: Activity, label: 'nav.activity', to: '/activity' },
]

/**
 * Everything that belongs to *this account*, kept apart from everything above.
 *
 * The group was "From YouTube" and held only the two mirrors. It now also holds
 * the profile and the YouTube connection, because those are the same subject —
 * somebody looking for "who am I and what is my account doing" was being sent
 * to Preferences on a phone and to nowhere at all on a desktop.
 *
 * The old heading carried a promise the new one cannot: that nothing here can
 * be edited. Watch later and Playlists are still a read-only copy of what the
 * member's YouTube account says, refreshed on every scan, and an edit made here
 * would be reverted by the next pass — §5's rule against a control that does not
 * do what it says. That promise now lives on those two items rather than on the
 * heading above them.
 */
const ACCOUNT: Item[] = [
  { icon: KeyRound, label: 'nav.youtubeAccount', to: '/account' },
  { icon: Clock, label: 'nav.watchLater', to: '/watch-later' },
  { icon: ListVideo, label: 'nav.playlists', to: '/playlists' },
]

/**
 * Where the sidebar can take you.
 *
 * Exported so the mobile bar can be checked against it. The two carry different
 * subsets — five fit across a phone, six down a rail — but a path in one that
 * exists in neither the router nor the other is a link to the not-found page,
 * and nothing about rendering it would say so.
 */
export const SIDEBAR_ROUTES = [...PRIMARY, ...ACCOUNT, { to: '/profile' }].map((i) => i.to)

function Row({ item, mini }: { item: Item; mini: boolean }) {
  const { icon: Icon, label, to } = item
  // The arrays above are module-level constants, so they carry keys rather than
  // words — a hook cannot be called where they are declared, and text baked in
  // there would never change language.
  const { t } = useTranslation()
  return (
    <PageNavLink
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
        {t(label)}
      </span>
    </PageNavLink>
  )
}

export function Sidebar({ mini }: { mini: boolean }) {
  const { t } = useTranslation()
  const { data: topics } = useTopics()
  const { data: subscriptions } = useSubscriptions()
  const { data: profiles } = useProfiles()
  const { id: profileID } = useCurrentProfile()
  // Whoever this browser is. Undefined only before the list has arrived, or on
  // a household of one that has never been asked — the row still draws, with
  // the neutral label, because the destination exists either way.
  const currentProfile = profiles?.find((p) => p.id === profileID) ?? profiles?.[0]

  if (mini) {
    return (
      <nav
        aria-label={t('ui.main')}
        className="fixed top-[var(--top-bar)] bottom-0 left-0 z-20 w-[72px] overflow-y-auto bg-bg py-1 no-scrollbar"
      >
        {[...PRIMARY, ...ACCOUNT].map((item) => (
          <Row key={item.to} item={item} mini />
        ))}
      </nav>
    )
  }

  return (
    <nav
      aria-label={t('ui.main')}
      className="fixed top-[var(--top-bar)] bottom-0 left-0 z-20 w-60 overflow-y-auto bg-bg px-3 py-3 no-scrollbar"
    >
      <section className="flex flex-col gap-0.5">
        {PRIMARY.map((item) => (
          <Row key={item.to} item={item} mini={false} />
        ))}
      </section>

      <hr className="my-3 border-0 border-t border-line" />
      <section className="flex flex-col gap-0.5">
        <h2 className="px-3 py-1.5 text-base font-medium">{t('nav.account')}</h2>
        {/* Who is watching, said with the avatar rather than a generic icon —
            the rail answers it without spending a line on it. Same 40px row and
            24px slot as every other item (MASTER.md §4), the avatar simply
            standing where the icon stands. */}
        <PageNavLink
          to="/profile"
          className={({ isActive }) =>
            clsx(
              'flex h-10 items-center gap-6 rounded-[10px] px-3 transition-colors duration-150 ease-out hover:bg-surface-hover',
              isActive && 'bg-surface-hover font-medium',
            )
          }
        >
          <Avatar hue={hueFromId(currentProfile?.id ?? '')} name={currentProfile?.name ?? '?'} size={24} />
          <span className="clamp-1 text-sm">{currentProfile?.name ?? t('nav.profile')}</span>
        </PageNavLink>
        {ACCOUNT.map((item) => (
          <Row key={item.to} item={item} mini={false} />
        ))}
      </section>

      {subscriptions && subscriptions.length > 0 && (
        <>
          <hr className="my-3 border-0 border-t border-line" />
          <section className="flex flex-col gap-0.5">
            <h2 className="px-3 py-1.5 text-base font-medium">{t('nav.subscriptions')}</h2>
            {subscriptions.map((channel) => (
              <PageNavLink
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
              </PageNavLink>
            ))}
          </section>
        </>
      )}

      {topics && topics.length > 0 && (
        <>
          <hr className="my-3 border-0 border-t border-line" />
          <section className="flex flex-col gap-0.5">
            <h2 className="px-3 py-1.5 text-base font-medium">{t('nav.topics')}</h2>
            {topics.map((topic) => (
              <PageNavLink
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
              </PageNavLink>
            ))}
          </section>
        </>
      )}
    </nav>
  )
}
