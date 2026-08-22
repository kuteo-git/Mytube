import type { ReactNode } from 'react'
import { Bookmark, CheckCircle, Share2, ThumbsDown, ThumbsUp } from 'lucide-react'
import {} from 'react-router-dom'
import type { Video } from '@/features/catalog/domain/video'
import { useSetPinned, useSetReaction, useSetSubscription } from '@/features/catalog/application/queries'
import { Avatar } from '@/shared/ui/primitives'
import { useCoarsePointer } from '@/shared/lib/pointer'
import { useToast } from '@/shared/ui/toast'
import {
  type ShareOutcome,
  shareURL,
  shareVideo,
} from '@/features/watch/application/share-link'

import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'
import { useFormat } from '@/shared/lib/useFormat'
import { useTranslation } from 'react-i18next'
import { PageLink } from '@/shared/ui/PageLink'

/**
 * Channel row plus the action cluster.
 *
 * "Ask" is out of scope. Subscribe registers the channel as a content source —
 * the scanner starts bringing its uploads in — so it is real, not decoration.
 *
 * Save is relabelled "Keep": it pins the video so the cache eviction sweep
 * will never reclaim it.
 */
export function VideoActions({ video, likeCount }: { video: Video; likeCount: number }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const reaction = video.userState?.reaction ?? 'NONE'
  const setReaction = useSetReaction(video.id)
  const setSubscription = useSetSubscription(video.channel.id)
  const setPinned = useSetPinned()

  // A share sheet where there is one, the clipboard where there is not.
  //
  // Asked of the pointer rather than of the screen's width: a share sheet is a
  // thing a touch device has, and a window narrowed on a desktop does not
  // acquire one. The same signal the player uses to tell a tap from a click.
  const coarse = useCoarsePointer()
  const toast = useToast()

  // What happened is said in a toast rather than on the button. Copying a link
  // changes nothing on screen, and a label that changes under the pointer is
  // read by whoever is already looking at that one control — while the eye at
  // that moment is on the thing being shared.
  const announce = (outcome: ShareOutcome) => {
    if (outcome === 'shared') toast(t('ui.shared'))
    else if (outcome === 'copied') toast(t('actions.linkCopied'))
    else if (outcome === 'failed') toast(t('actions.couldNotCopy'))
    // A cancelled share sheet is the viewer's own answer, and reporting a
    // decision back to the person who made it is noise.
  }

  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex items-center gap-3">
        <PageLink to={`/channel/${video.channel.id}`}>
          <Avatar
            hue={hueFromId(video.channel.id)}
            name={video.channel.name}
            src={mediaURL(video.channel.avatarPath)}
            size={40}
          />
        </PageLink>
        <div>
          <PageLink to={`/channel/${video.channel.id}`} className="flex items-center gap-1 text-base font-medium">
            {video.channel.name}
            {video.channel.verified && <CheckCircle size={14} className="text-text-2" />}
          </PageLink>
          <p className="text-xs text-text-2">{fmt.subscribers(video.channel.subscriberCount)}</p>
        </div>
        <button
          type="button"
          aria-pressed={video.channel.subscribed}
          disabled={setSubscription.isPending}
          onClick={() => setSubscription.mutate(!video.channel.subscribed)}
          className={
            'ml-2 rounded-full px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out disabled:opacity-60 ' +
            (video.channel.subscribed
              ? 'bg-surface hover:bg-surface-hover'
              : 'bg-text text-bg hover:bg-text/90')
          }
        >
          {video.channel.subscribed ? t('ui.subscribed') : t('ui.subscribe')}
        </button>
      </div>

      <div className="flex items-center gap-2">
        <div className="flex h-9 items-center rounded-full bg-surface">
          <button
            type="button"
            aria-pressed={reaction === 'LIKE'}
            aria-label={t('ui.like')}
            disabled={setReaction.isPending}
            onClick={() => setReaction.mutate(reaction === 'LIKE' ? 'NONE' : 'LIKE')}
            className="flex h-full items-center gap-2 rounded-l-full px-4 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            <ThumbsUp size={20} fill={reaction === 'LIKE' ? 'currentColor' : 'none'} />
            {fmt.count(likeCount)}
          </button>
          <span className="h-6 w-px bg-line-subtle" />
          <button
            type="button"
            aria-pressed={reaction === 'DISLIKE'}
            aria-label={t('ui.dislike')}
            disabled={setReaction.isPending}
            onClick={() => setReaction.mutate(reaction === 'DISLIKE' ? 'NONE' : 'DISLIKE')}
            className="grid h-full w-12 place-items-center rounded-r-full transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            <ThumbsDown size={20} fill={reaction === 'DISLIKE' ? 'currentColor' : 'none'} />
          </button>
        </div>

        {/* Says what happened, in a toast. A copy with nothing to show for it
            is a button that appears not to work, which is how this one was
            reported twice. */}
        <ActionPill
          icon={<Share2 size={20} />}
          label={t('ui.share')}
          onClick={() => {
            void shareVideo({
              url: shareURL(video),
              title: video.title,
              canShare: coarse,
            }).then(announce)
          }}
        />
        {/* No Watch later button. That list is a read-only mirror of the
            member's YouTube account, refreshed on every account scan, so a
            press here would be undone by the next pass. */}
        <ActionPill
          icon={<Bookmark size={20} fill={video.pinned ? 'currentColor' : 'none'} />}
          label={video.pinned ? t('common.saved') : t('common.save')}
          onClick={() => setPinned.mutate({ videoId: video.id, pinned: !video.pinned })}
        />

        {/* No overflow menu. It opened nothing — a button that looks like a
            control and is not one is the single thing §5 forbids outright. */}
      </div>
    </div>
  )
}

function ActionPill({
  icon,
  label,
  onClick,
}: {
  icon: ReactNode
  label: string
  onClick?: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex h-9 items-center gap-2 rounded-full bg-surface px-4 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
    >
      {icon}
      {label}
    </button>
  )
}
