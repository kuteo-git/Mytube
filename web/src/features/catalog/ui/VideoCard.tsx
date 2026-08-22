import clsx from 'clsx'
import { videoItemBleed, videoItemHover } from '@/features/catalog/ui/video-item-hover'
import { Bookmark, BookmarkMinus, CheckCircle, EyeOff, MoreVertical } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import {} from 'react-router-dom'
import type { Video } from '../domain/video'
import { watchProgress } from '../domain/video'
import {
  useMarkWatched,
  useNotInterested,
  useSetPinned,
  useStreamPrefetch,
} from '../application/queries'
import { Avatar, ThumbnailSurface } from '@/shared/ui/primitives'

import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'
import { useCoarsePointer } from '@/shared/lib/pointer'
import { useToast } from '@/shared/ui/toast'
import { useFormat } from '@/shared/lib/useFormat'
import { useTranslation } from 'react-i18next'
import { PageLink } from '@/shared/ui/PageLink'

export type VideoCardVariant =
  | 'continueWatching'
  | 'feed'
  | 'saved'
  // Watch later and the playlists are one variant, not two. Both are read-only
  // mirrors of the member's YouTube account, so a card in either offers exactly
  // the same things — and two variants would be two chances for them to drift.
  | 'fromYouTube'
  | 'history'
  | 'storage'

/** Card. Hover comes from videoItemHover, shared with every other item. */
export function VideoCard({
  video,
  variant = 'feed',
  queueSearch = '',
}: {
  video: Video
  variant?: VideoCardVariant
  /**
   * `?list=…` to append to this card's links, so opening the video plays the
   * list it came from rather than an unrelated recommendation. Empty means the
   * video is opened on its own.
   */
  queueSearch?: string
}) {
  const { t } = useTranslation()
  const fmt = useFormat()
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
      <PageLink to={`/watch/${video.id}${queueSearch}`} tabIndex={-1} aria-hidden className="block">
        <ThumbnailSurface hue={hueFromId(video.id)} src={mediaURL(video.thumbnailPath)} alt={video.title} channelName={video.channel.name}>
          {video.reason === 'DISCOVERY' && (
            <span className="absolute top-2 left-2 rounded bg-badge px-1.5 py-0.5 text-xs font-medium">
              {t('card.suggested')}
            </span>
          )}
          {/* Nothing at all when the length is not known.
              
              A flat listing carries no duration, so plenty of rows arrive with
              zero — and "0:00" is not a shorter way of saying "we don't know",
              it is a claim that the video is empty. ExternalVideoCard and
              QueueRail already drew nothing in that case; this card and the up
              next rail did not, which is the sort of disagreement nobody sees
              until they are looking at both at once.
              
              A broadcast is the exception that made this visible: it carries
              zero *and* has something to say. Same corner and same box as the
              duration it replaces, so a grid holding both does not step. */}
          {(video.isLiveNow || video.durationSeconds > 0) && (
            <span
              className={clsx(
                'absolute right-1.5 bottom-1.5 rounded px-1 py-0.5 text-xs font-medium',
                video.isLiveNow
                  ? 'bg-brand text-white'
                  : 'bg-badge tabular-nums',
              )}
            >
              {video.isLiveNow ? 'LIVE' : fmt.duration(video.durationSeconds)}
            </span>
          )}
          {progress > 0 && (
            <span className="absolute inset-x-0 bottom-0 h-1 bg-white/30">
              <span
                className="block h-full bg-brand"
                style={{ width: `${Math.round(progress * 100)}%` }}
              />
            </span>
          )}
        </ThumbnailSurface>
      </PageLink>

      <div className="flex gap-3">
        {/* The picture goes where the name goes.
            It is a channel's face sitting next to a link bearing that channel's
            name, so it reads as the same control — and it was the only half
            that did nothing. The watch page's byline already links both
            (VideoActions), which is what made the difference visible: the same
            avatar was live there and dead here.
            `shrink-0` because the row is `flex` and a long title would
            otherwise squeeze the anchor narrower than the picture inside it. */}
        <PageLink
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
        </PageLink>

        <div className="min-w-0 flex-1">
          <h3 className="clamp-2 text-sm leading-5 font-medium">
            <PageLink to={`/watch/${video.id}${queueSearch}`}>{video.title}</PageLink>
          </h3>
          <p className="mt-1 flex items-center gap-1 text-xs text-text-2">
            <PageLink to={`/channel/${video.channel.id}`} className="hover:text-text">
              {video.channel.name}
            </PageLink>
            {video.channel.verified && <CheckCircle size={12} aria-label={t('ui.verified')} />}
          </p>
          <p className="text-xs text-text-2">{describeVideo(video, fmt)}</p>
        </div>

        <div className="relative" ref={menuRef}>
          <button
            type="button"
            aria-label={t('common.moreOptions')}
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
function describeVideo(video: Video, fmt: ReturnType<typeof useFormat>): string {
  const parts: string[] = []
  if (video.viewCount > 0) parts.push(fmt.views(video.viewCount))
  if (video.publishedAt) parts.push(fmt.relative(video.publishedAt))
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
  const { t } = useTranslation()
  const notInterested = useNotInterested()
  const markWatched = useMarkWatched()
  const setPinned = useSetPinned()
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
          toast(t('card.markedWatched'))
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <CheckCircle size={16} />
        {t('card.watched')}
      </button>
    </li>
  )

  const notInterestedItem = (
    <li key="not-interested">
      <button
        type="button"
        onClick={() => {
          notInterested.mutate(video.id)
          toast(t('card.notInterested'))
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <EyeOff size={16} />{t('card.notInterested')}</button>
    </li>
  )

  const saveItem = (
    <li key="save">
      <button
        type="button"
        onClick={() => {
          setPinned.mutate({ videoId: video.id, pinned: true })
          toast(t('common.saved'))
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <Bookmark size={16} />
        {t('common.save')}
      </button>
    </li>
  )

  const unsaveItem = (
    <li key="unsave">
      <button
        type="button"
        onClick={() => {
          setPinned.mutate({ videoId: video.id, pinned: false })
          toast(t('ui.unsaved'))
          close()
        }}
        className="flex w-full items-center gap-3 px-4 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <BookmarkMinus size={16} />
        {t('card.unsave')}
      </button>
    </li>
  )

  // Putting a video aside and keeping its bytes are two different intentions,
  // so they are two entries. Watch later is a note about this evening; Save is
  // an instruction to the eviction sweep.
  // No "add to playlist" and no "remove from playlist". Watch later and the
  // playlists are a read-only mirror of the member's YouTube account, refreshed
  // on every account scan: an edit made here would be reverted by the next pass,
  // which is worse than not offering it.

  switch (variant) {
    case 'continueWatching':
      items.push(watchedItem, notInterestedItem)
      break
    case 'feed':
      items.push(watchedItem, saveItem, notInterestedItem)
      break
    case 'saved':
      items.push(unsaveItem)
      break
    // A mirror of YouTube, so the menu offers only what belongs to this
    // library: marking it watched, and keeping its bytes.
    case 'fromYouTube':
      items.push(watchedItem, saveItem)
      break
    case 'history':
      items.push(saveItem)
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
