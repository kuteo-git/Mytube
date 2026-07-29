import clsx from 'clsx'
import { CheckCircle, MoreVertical } from 'lucide-react'
import { Link } from 'react-router-dom'
import type { Video } from '../domain/video'
import { watchProgress } from '../domain/video'
import { useStreamPrefetch } from '../application/queries'
import { Avatar, ThumbnailSurface } from '@/shared/ui/primitives'
import { formatDuration, formatRelative, formatViews } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'

/**
 * Grid card. No scale or shadow on hover: youtube.com does not do it and a
 * transform here would shift the grid (see MASTER.md anti-patterns).
 */
export function VideoCard({ video }: { video: Video }) {
  const progress = watchProgress(video)
  const { prefetch, cancel } = useStreamPrefetch()

  return (
    // Resolving an upstream URL takes over a second, and it is the whole of the
    // delay before a video starts. Resting on a card is a good enough signal to
    // spend it here, where nobody is waiting, instead of after the click.
    // Focus counts too, so a keyboard or a remote gets the same head start.
    <article
      className="group flex flex-col gap-3"
      onPointerEnter={() => prefetch(video.id)}
      onPointerLeave={cancel}
      onFocus={() => prefetch(video.id)}
      onBlur={cancel}
    >
      <Link to={`/watch/${video.id}`} tabIndex={-1} aria-hidden className="block">
        <ThumbnailSurface hue={hueFromId(video.id)} src={video.thumbnailPath} alt={video.title}>
          {video.mediaState === 'DOWNLOADING' && (
            <span className="absolute top-2 left-2 rounded bg-badge px-1.5 py-0.5 text-xs font-medium">
              Downloading…
            </span>
          )}
          <span className="absolute right-1.5 bottom-1.5 rounded bg-badge px-1 py-0.5 text-xs font-medium tabular-nums">
            {formatDuration(video.durationSeconds)}
          </span>
          {progress > 0 && (
            <span className="absolute inset-x-0 bottom-0 h-1 bg-white/30">
              <span
                className="block h-full bg-brand"
                style={{ width: `${Math.round(progress * 100)}%` }}
              />
            </span>
          )}
        </ThumbnailSurface>
      </Link>

      <div className="flex gap-3">
        <Avatar hue={hueFromId(video.channel.id)} name={video.channel.name} size={36} />

        <div className="min-w-0 flex-1">
          <h3 className="clamp-2 text-sm leading-5 font-medium">
            <Link to={`/watch/${video.id}`}>{video.title}</Link>
          </h3>
          <p className="mt-1 flex items-center gap-1 text-xs text-text-2">
            <Link to={`/channel/${video.channel.id}`} className="hover:text-text">
              {video.channel.name}
            </Link>
            {video.channel.verified && <CheckCircle size={12} aria-label="Verified" />}
          </p>
          <p className="text-xs text-text-2">{describeVideo(video)}</p>
        </div>

        <button
          type="button"
          aria-label="More options"
          className={clsx(
            'h-9 w-9 shrink-0 rounded-full transition-opacity duration-150 ease-out',
            // Revealed on hover like youtube.com, but also on keyboard focus so
            // the action stays reachable without a pointer (TV remote, Phase 3).
            'opacity-0 group-hover:opacity-100 focus-visible:opacity-100',
            'grid place-items-center hover:bg-surface-hover',
          )}
        >
          <MoreVertical size={20} />
        </button>
      </div>
    </article>
  )
}

/**
 * Scanned videos have no view count or upload date: flat listings omit both.
 * Printing "0 views • 1 minute ago" for all of them would be a plausible lie,
 * so each part appears only when it is actually known.
 */
function describeVideo(video: Video): string {
  const parts: string[] = []
  if (video.viewCount > 0) parts.push(formatViews(video.viewCount))
  if (video.publishedAt) parts.push(formatRelative(video.publishedAt))
  return parts.join(' • ')
}

export function VideoCardSkeleton() {
  return (
    <div className="flex animate-pulse flex-col gap-3">
      <div className="aspect-video w-full rounded-xl bg-surface" />
      <div className="flex gap-3">
        <div className="h-9 w-9 shrink-0 rounded-full bg-surface" />
        <div className="flex-1 space-y-2">
          <div className="h-4 w-full rounded bg-surface" />
          <div className="h-3 w-2/3 rounded bg-surface" />
        </div>
      </div>
    </div>
  )
}
