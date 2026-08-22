import { ChevronRight } from 'lucide-react'
import {} from 'react-router-dom'
import { useSubscriptions } from '@/features/catalog/application/queries'
import { Avatar } from '@/shared/ui/primitives'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'
import { useTranslation } from 'react-i18next'
import { PageLink } from '@/shared/ui/PageLink'

/**
 * The channels you follow, as a page of their own.
 *
 * A phone has no sidebar, so until now the only way to a channel was to find
 * one of its videos and tap through — which is a poor way to answer "what has
 * this channel posted", the question following a channel is for.
 *
 * Channels rather than a feed of their latest videos. That is what YouTube puts
 * here, and it would need catalog to be able to rank a feed filtered by
 * subscription — work in the service, not in this file. This answers the
 * question actually asked: which channels, and take me to one.
 */
export function SubscriptionsPage() {
  const { t } = useTranslation()
  const { data: channels, isPending, isError } = useSubscriptions()

  if (isError) {
    return (
      <p className="py-16 text-center text-text-2">
        {t('empty.couldNotReachLibrary')}
      </p>
    )
  }

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <h1 className="py-4 text-2xl font-bold">{t('nav.subscriptions')}</h1>

      {isPending ? (
        <ul className="flex flex-col">
          {Array.from({ length: 6 }, (_, i) => (
            <li key={i} className="flex items-center gap-3 py-3">
              <div className="h-11 w-11 shrink-0 animate-pulse rounded-full bg-surface" />
              <div className="h-4 flex-1 animate-pulse rounded bg-surface" />
            </li>
          ))}
        </ul>
      ) : channels && channels.length > 0 ? (
        <ul className="flex flex-col">
          {channels.map((channel) => (
            <li key={channel.id}>
              <PageLink
                to={`/channel/${channel.id}`}
                // 44px of height at least, which the 44px avatar and the
                // padding around it clear on their own.
                className="flex items-center gap-3 rounded-xl py-3
                           transition-colors duration-150 ease-out hover:bg-surface-hover"
              >
                <Avatar
                  hue={hueFromId(channel.id)}
                  name={channel.name}
                  src={mediaURL(channel.avatarPath)}
                  size={44}
                />
                <span className="min-w-0 flex-1">
                  <span className="clamp-1 block text-sm font-medium">{channel.name}</span>
                  {channel.handle && (
                    <span className="clamp-1 block text-xs text-text-2">{channel.handle}</span>
                  )}
                </span>
                <ChevronRight size={18} className="shrink-0 text-text-2" />
              </PageLink>
            </li>
          ))}
        </ul>
      ) : (
        // Says how to get one rather than only that there are none. Subscribing
        // happens on a channel's own page, which is not somewhere you would
        // guess from here.
        <p className="py-16 text-center text-sm text-text-2">
          {t('empty.subscriptions')}
        </p>
      )}
    </div>
  )
}
