import { ListVideo } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { Video } from '../domain/video'
import { topPlayedQueueSearch } from '@/features/watch/application/queue'
import { ThumbnailSurface } from '@/shared/ui/primitives'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'

/**
 * The most-played collection, presented as a stack of cards the way youtube.com
 * presents a mix — the shape says "this is a list, not a video" before anything
 * is read.
 *
 * Clicking it starts the first video with the whole collection as the queue, so
 * it plays from most-watched downwards.
 */
export function TopPlayedCard({ videos }: { videos: Video[] }) {
  if (videos.length === 0) return null

  const lead = videos[0]
  const names = videos
    .slice(0, 3)
    .map((v) => v.channel.name)
    .filter((name, i, all) => name && all.indexOf(name) === i)

  return (
    <article className="group flex flex-col gap-3">
      <Link to={`/watch/${lead.id}${topPlayedQueueSearch()}`} className="block">
        {/* The offset slivers behind the thumbnail are the stack. Purely
            decorative, so they are hidden from assistive technology. */}
        <div className="relative pt-2">
          <span
            aria-hidden
            className="absolute inset-x-3 top-0 h-2 rounded-t-lg bg-white/15"
          />
          <span
            aria-hidden
            className="absolute inset-x-1.5 top-1 h-2 rounded-t-lg bg-white/25"
          />
          <ThumbnailSurface hue={hueFromId(lead.id)} src={mediaURL(lead.thumbnailPath)} alt="">
            <span className="absolute right-1.5 bottom-1.5 flex items-center gap-1.5 rounded bg-badge px-2 py-1 text-xs font-medium">
              <ListVideo size={14} />
              Mix
            </span>
          </ThumbnailSurface>
        </div>
      </Link>

      <div className="min-w-0">
        <h3 className="clamp-2 text-sm leading-5 font-medium">
          <Link to={`/watch/${lead.id}${topPlayedQueueSearch()}`}>
            Mix — the {videos.length} you play most
          </Link>
        </h3>
        {names.length > 0 && (
          <p className="mt-1 clamp-1 text-xs text-text-2">
            {names.join(', ')}
            {videos.length > names.length && ', and more'}
          </p>
        )}
      </div>
    </article>
  )
}
