import clsx from 'clsx'
import { videoItemHover } from '@/features/catalog/ui/video-item-hover'
import { Loader2 } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import type { ExternalVideo } from '../infrastructure/catalogRepository'
import { useOpenExternal } from '../application/queries'
import { ThumbnailSurface } from '@/shared/ui/primitives'
import { formatDuration, formatRelative, formatViews } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'

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

  return (
    <article className={clsx('flex flex-col gap-3', videoItemHover)}>
      <button
        type="button"
        disabled={open.isPending}
        onClick={() =>
          open.mutate(video.sourceUrl, {
            onSuccess: (videoId) => navigate(`/watch/${videoId}${queueSearch}`),
          })
        }
        className="block text-left"
      >
        <ThumbnailSurface hue={hueFromId(video.id)} src={video.thumbnailUrl} alt={video.title}>
          {video.durationSeconds > 0 && (
            <span className="absolute right-1.5 bottom-1.5 rounded bg-badge px-1 py-0.5 text-xs font-medium tabular-nums">
              {formatDuration(video.durationSeconds)}
            </span>
          )}
          {video.inLibrary && (
            <span className="absolute top-2 left-2 rounded bg-badge px-1.5 py-0.5 text-xs font-medium">
              In library
            </span>
          )}
          {open.isPending && (
            <span className="absolute inset-0 grid place-items-center bg-black/55">
              <Loader2 size={20} className="animate-spin" />
            </span>
          )}
        </ThumbnailSurface>

        <h3 className="clamp-2 mt-3 text-sm leading-5 font-medium">{video.title}</h3>
        {video.channelName && <p className="mt-1 text-xs text-text-2">{video.channelName}</p>}
        {/* Views and age share one line separated by a dot, as on youtube.com.
            Each half appears only when known, so a source that discloses
            neither leaves no empty row behind. */}
        {describeExternal(video) && (
          <p className="text-xs text-text-2">{describeExternal(video)}</p>
        )}
      </button>

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
