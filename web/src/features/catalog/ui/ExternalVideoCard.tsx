import clsx from 'clsx'
import { videoItemBleed, videoItemHover } from '@/features/catalog/ui/video-item-hover'
import { Bookmark, Loader2, MoreVertical } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import type { ExternalVideo } from '../infrastructure/catalogRepository'
import { useOpenExternal, useSetPinned } from '../application/queries'
import { ThumbnailSurface } from '@/shared/ui/primitives'
import { formatDuration, formatRelative, formatViews } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'
import { useCoarsePointer } from '@/shared/lib/pointer'
import { useToast } from '@/shared/ui/toast'

/**
 * A video that lives upstream and may not have a catalog row yet.
 *
 * Opening it writes the metadata first, then navigates; the download itself
 * starts when the player asks how to play it. Used by search results and by the
 * channel page, both of which list videos straight from YouTube rather than
 * from the local library.
 */
export function ExternalVideoCard({
  video,
  queueSearch = '',
}: {
  video: ExternalVideo
  /**
   * Appended to the watch link so the video opens as part of a list. Without
   * it the video plays alone and "next" falls back to recommendations.
   */
  queueSearch?: string
}) {
  const navigate = useNavigate()
  const open = useOpenExternal()
  const setPinned = useSetPinned()
  const coarse = useCoarsePointer()
  const toast = useToast()
  const [menuOpen, setMenuOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!menuOpen) return
    const closeMenu = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false)
      }
    }
    document.addEventListener('click', closeMenu)
    return () => document.removeEventListener('click', closeMenu)
  }, [menuOpen])

  const go = () =>
    open.mutate(video.sourceUrl, {
      onSuccess: (videoId) => navigate(`/watch/${videoId}${queueSearch}`),
    })

  return (
    <article className={clsx('group flex flex-col gap-3', videoItemHover, videoItemBleed)}>
      <button
        type="button"
        disabled={open.isPending}
        onClick={go}
        className="block text-left"
      >
        <ThumbnailSurface hue={hueFromId(video.id)} src={video.thumbnailUrl} alt={video.title} channelName={video.channelName}>
          {video.durationSeconds > 0 && (
            <span className="absolute right-1.5 bottom-1.5 rounded bg-badge px-1 py-0.5 text-xs font-medium tabular-nums">
              {formatDuration(video.durationSeconds)}
            </span>
          )}
          {open.isPending && (
            <span className="absolute inset-0 grid place-items-center bg-black/55">
              <Loader2 size={20} className="animate-spin" />
            </span>
          )}
        </ThumbnailSurface>
      </button>

      <div className="flex gap-3">
        <div className="min-w-0 flex-1">
          <h3 className="clamp-2 text-sm leading-5 font-medium">
            <button type="button" disabled={open.isPending} onClick={go} className="text-left">
              {video.title}
            </button>
          </h3>
          {video.channelName && <p className="mt-1 text-xs text-text-2">{video.channelName}</p>}
          {describeExternal(video) && (
            <p className="text-xs text-text-2">{describeExternal(video)}</p>
          )}
        </div>

        <div className="relative" ref={menuRef}>
          <button
            type="button"
            aria-label="More options"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((o) => !o)}
            className={clsx(
              'shrink-0 rounded-full transition-opacity duration-150 ease-out',
              coarse ? 'h-11 w-11' : 'h-9 w-9',
              coarse ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 focus-visible:opacity-100',
              'grid place-items-center text-text-2 hover:bg-white/10 hover:text-text',
              menuOpen && 'opacity-100 bg-white/10',
            )}
          >
            <MoreVertical size={20} />
          </button>
          {menuOpen && (
            <ul className="absolute right-0 bottom-10 z-40 min-w-36 overflow-hidden rounded-lg bg-surface py-1 text-sm shadow-lg ring-1 ring-line">
              <li>
                <button
                  type="button"
                  disabled={saving}
                  onClick={async () => {
                    setSaving(true)
                    try {
                      const videoId = await open.mutateAsync(video.sourceUrl)
                      setPinned.mutate({ videoId, pinned: true })
                      toast('Saved')
                    } catch {
                      // Video may already exist; try standard save
                    }
                    setSaving(false)
                    setMenuOpen(false)
                  }}
                  className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover disabled:opacity-50"
                >
                  {saving ? <Loader2 size={16} className="animate-spin" /> : <Bookmark size={16} />}
                  Save
                </button>
              </li>
            </ul>
          )}
        </div>
      </div>

      {open.isError && <p className="text-xs text-brand">Could not open that video.</p>}
    </article>
  )
}

/**
 * Views and upload age, each omitted when the source did not disclose it.
 *
 * Upload dates from YouTube's own listing are relative to begin with ("2 years
 * ago"), so they are stored as an approximate instant and rendered relatively
 * again — which is both what YouTube shows and the only precision that is
 * actually there.
 */
function describeExternal(video: ExternalVideo): string {
  const parts: string[] = []
  if (video.viewCount > 0) parts.push(formatViews(video.viewCount))
  if (video.publishedAt) parts.push(formatRelative(video.publishedAt))
  return parts.join(' • ')
}
