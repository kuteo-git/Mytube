import clsx from 'clsx'
import { videoItemBleed, videoItemHover } from '@/features/catalog/ui/video-item-hover'
import {
  Bookmark,
  BookmarkMinus,
  CheckCircle,
  Clock,
  ClockFading,
  EyeOff,
  MoreVertical,
} from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import type { Video } from '../domain/video'
import { watchProgress } from '../domain/video'
import {
  useMarkWatched,
  useNotInterested,
  useSetPinned,
  useSetWatchLater,
  useStreamPrefetch,
} from '../application/queries'
import { Avatar, ThumbnailSurface } from '@/shared/ui/primitives'
import { formatDuration, formatRelative, formatViews } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'
import { useCoarsePointer } from '@/shared/lib/pointer'
import { useToast } from '@/shared/ui/toast'

export type VideoCardVariant =
  | 'continueWatching'
  | 'feed'
  | 'saved'
  | 'watchLater'
  | 'history'
  | 'storage'

/** Card. Hover comes from videoItemHover, shared with every other item. */
export function VideoCard({ video, variant = 'feed' }: { video: Video; variant?: VideoCardVariant }) {
  const progress = watchProgress(video)
  const { prefetch, cancel } = useStreamPrefetch()
  const [menuOpen, setMenuOpen] = useState(false)
  const coarse = useCoarsePointer()
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!menuOpen) return
    const close = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false)
      }
    }
    document.addEventListener('click', close)
    return () => document.removeEventListener('click', close)
  }, [menuOpen])

  return (
    // Resolving an upstream URL takes over a second, and it is the whole of the
    // delay before a video starts. Resting on a card is a good enough signal to
    // spend it here, where nobody is waiting, instead of after the click.
    // Focus counts too, so a keyboard or a remote gets the same head start.
    <article
      className={clsx('group flex flex-col gap-3', videoItemHover, videoItemBleed)}
      onPointerEnter={() => prefetch(video.id)}
      onPointerLeave={cancel}
      onFocus={() => prefetch(video.id)}
      onBlur={cancel}
    >
      <Link to={`/watch/${video.id}`} tabIndex={-1} aria-hidden className="block">
        <ThumbnailSurface hue={hueFromId(video.id)} src={mediaURL(video.thumbnailPath)} alt={video.title} channelName={video.channel.name}>
          {video.reason === 'DISCOVERY' && (
            <span className="absolute top-2 left-2 rounded bg-badge px-1.5 py-0.5 text-xs font-medium">
              Suggested
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
        {/* The picture goes where the name goes.
            It is a channel's face sitting next to a link bearing that channel's
            name, so it reads as the same control — and it was the only half
            that did nothing. The watch page's byline already links both
            (VideoActions), which is what made the difference visible: the same
            avatar was live there and dead here.
            `shrink-0` because the row is `flex` and a long title would
            otherwise squeeze the anchor narrower than the picture inside it. */}
        <Link
          to={`/channel/${video.channel.id}`}
          className="shrink-0 self-start rounded-full"
          aria-label={video.channel.name}
        >
          <Avatar
            hue={hueFromId(video.channel.id)}
            name={video.channel.name}
            src={mediaURL(video.channel.avatarPath)}
            size={36}
          />
        </Link>

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

        <div className="relative" ref={menuRef}>
          <button
            type="button"
            aria-label="More options"
            aria-expanded={menuOpen}
            onClick={() => setMenuOpen((o) => !o)}
            className={clsx(
              'shrink-0 rounded-full transition-opacity duration-150 ease-out',
              coarse ? 'h-11 w-11' : 'h-9 w-9',
              // Revealed on hover like youtube.com, and on keyboard focus so the
              // action stays reachable without a pointer (TV remote, Phase 3).
              //
              // Always there for a finger. There is no hovering on a touch
              // screen, so hiding it behind hover did not make it discreet, it
              // made it unreachable — the menu simply did not exist on a phone.
              coarse ? 'opacity-100' : 'opacity-0 group-hover:opacity-100 focus-visible:opacity-100',
              // text-2 at rest like every other icon button in the app; it was
              // inheriting full white, which made it the brightest thing on a
              // card whose own title is the point.
              //
              // Its own hover is white/10 rather than surface-hover: the card
              // underneath is surface-hover by the time this button can be
              // hovered at all, so the same colour would be no feedback.
              'grid place-items-center text-text-2 hover:bg-white/10 hover:text-text',
              menuOpen && 'opacity-100 bg-white/10',
            )}
          >
            <MoreVertical size={20} />
          </button>
          {menuOpen && <CardMenu video={video} variant={variant} close={() => setMenuOpen(false)} />}
        </div>
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

/**
 * The card's overflow menu.
 *
 * Keep is not here. Keeping a video is a decision about one video you have
 * chosen to watch, and it lives on the watch page beside the other actions on
 * it. Offered from a grid of two dozen it is a decision nobody came to make,
 * sitting above the two that are actually about tidying the grid.
 */
function CardMenu({
  video,
  variant,
  close,
}: {
  video: Video
  variant: VideoCardVariant
  close: () => void
}) {
  const notInterested = useNotInterested()
  const markWatched = useMarkWatched()
  const setPinned = useSetPinned()
  const setWatchLater = useSetWatchLater()
  const toast = useToast()

  const items: React.ReactNode[] = []

  const watchedItem = (
    <li key="watched">
      <button
        type="button"
        onClick={() => {
          markWatched.mutate({
            videoId: video.id,
            durationSeconds: video.durationSeconds,
          })
          toast('Marked as watched')
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <CheckCircle size={16} />
        Watched
      </button>
    </li>
  )

  const notInterestedItem = (
    <li key="not-interested">
      <button
        type="button"
        onClick={() => {
          notInterested.mutate(video.id)
          toast('Not interested')
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <EyeOff size={16} />
        Not interested
      </button>
    </li>
  )

  const saveItem = (
    <li key="save">
      <button
        type="button"
        onClick={() => {
          setPinned.mutate({ videoId: video.id, pinned: true })
          toast('Saved')
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <Bookmark size={16} />
        Save
      </button>
    </li>
  )

  const unsaveItem = (
    <li key="unsave">
      <button
        type="button"
        onClick={() => {
          setPinned.mutate({ videoId: video.id, pinned: false })
          toast('Unsaved')
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <BookmarkMinus size={16} />
        Unsave
      </button>
    </li>
  )

  // Putting a video aside and keeping its bytes are two different intentions,
  // so they are two entries. Watch later is a note about this evening; Save is
  // an instruction to the eviction sweep.
  const watchLaterItem = (
    <li key="watch-later">
      <button
        type="button"
        onClick={() => {
          setWatchLater.mutate({ videoId: video.id, inWatchLater: true })
          toast('Added to Watch later')
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <Clock size={16} />
        Watch later
      </button>
    </li>
  )

  const removeWatchLaterItem = (
    <li key="remove-watch-later">
      <button
        type="button"
        onClick={() => {
          setWatchLater.mutate({ videoId: video.id, inWatchLater: false })
          toast('Removed from Watch later')
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <ClockFading size={16} />
        Remove from Watch later
      </button>
    </li>
  )

  switch (variant) {
    case 'continueWatching':
      items.push(watchedItem, notInterestedItem)
      break
    case 'feed':
      items.push(watchedItem, watchLaterItem, saveItem, notInterestedItem)
      break
    case 'saved':
      items.push(watchLaterItem, unsaveItem)
      break
    case 'watchLater':
      items.push(watchedItem, removeWatchLaterItem, saveItem)
      break
    case 'history':
      items.push(watchLaterItem, saveItem)
      break
    case 'storage':
      items.push(saveItem)
      break
  }

  return (
    <ul className="absolute right-0 bottom-10 z-40 min-w-40 overflow-hidden rounded-lg bg-surface py-1 text-sm shadow-lg ring-1 ring-line">
      {items}
    </ul>
  )
}
