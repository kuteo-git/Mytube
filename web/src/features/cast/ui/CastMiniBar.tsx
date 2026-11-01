import { Cast } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { mediaURL } from '@/shared/lib/media'
import { currentItem } from '../domain/cast'
import { useCast } from '../application/cast-context'
import { useCastSessions } from '../application/queries'

/**
 * "Something is playing on a speaker", on every page, for whoever is in the
 * house: a session started on another phone is just as much this one's to
 * pause. Pressing it opens the remote.
 *
 * Above the page's bottom edge by `bottomOffset`, which the shell computes from
 * the tab bar and the miniplayer so the three never overlap.
 */
export function CastMiniBar({ bottomOffset }: { bottomOffset: number }) {
  const { t } = useTranslation()
  const { remoteSessionId, openRemote } = useCast()
  const { data: sessions } = useCastSessions()
  const session = sessions?.[0]
  if (!session || remoteSessionId) return null
  const item = currentItem(session)

  return (
    <button
      type="button"
      aria-label={t('cast.openRemote')}
      onClick={() => openRemote(session.id)}
      style={{ bottom: bottomOffset }}
      className="fixed left-1/2 z-[45] flex h-12 w-[min(28rem,calc(100%-2rem))] -translate-x-1/2 items-center gap-3 rounded-full border border-line bg-surface px-3 text-left shadow-lg hover:bg-surface-hover"
    >
      <Cast size={18} className="shrink-0 text-text" />
      {item && mediaURL(item.thumbnailUrl) && <img src={mediaURL(item.thumbnailUrl)} alt="" className="h-8 w-14 shrink-0 rounded object-cover" />}
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm text-text">{item?.title}</span>
        <span className="block truncate text-xs text-text-2">
          {session.state === 'paused'
            ? t('cast.pausedOn', { device: session.device.name })
            : t('cast.playingOn', { device: session.device.name })}
        </span>
      </span>
    </button>
  )
}
