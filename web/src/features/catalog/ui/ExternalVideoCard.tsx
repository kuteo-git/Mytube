import { Loader2 } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import type { ExternalVideo } from '../infrastructure/catalogRepository'
import { useOpenExternal } from '../application/queries'
import { ThumbnailSurface } from '@/shared/ui/primitives'
import { formatDuration, formatViews } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'

/**
 * A video that lives upstream and may not have a catalog row yet.
 *
 * Opening it writes the metadata first, then navigates; the download itself
 * starts when the player asks how to play it. Used by search results and by the
 * channel page, both of which list videos straight from YouTube rather than
 * from the local library.
 */
export function ExternalVideoCard({ video }: { video: ExternalVideo }) {
  const navigate = useNavigate()
  const open = useOpenExternal()

  return (
    <article className="flex flex-col gap-3">
      <button
        type="button"
        disabled={open.isPending}
        onClick={() =>
          open.mutate(video.sourceUrl, {
            onSuccess: (videoId) => navigate(`/watch/${videoId}`),
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
        <p className="mt-1 text-xs text-text-2">{video.channelName}</p>
        {video.viewCount > 0 && <p className="text-xs text-text-2">{formatViews(video.viewCount)}</p>}
      </button>

      {open.isError && <p className="text-xs text-brand">Could not open that video.</p>}
    </article>
  )
}
