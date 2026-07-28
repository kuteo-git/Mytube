import type { ReactNode } from 'react'
import { Bookmark, CheckCircle, MoreHorizontal, Share2, ThumbsDown, ThumbsUp } from 'lucide-react'
import type { Video } from '@/features/catalog/domain/video'
import { useSetReaction, useSetSubscription } from '@/features/catalog/application/queries'
import { Avatar } from '@/shared/ui/primitives'
import { formatCount, formatSubscribers } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'

/**
 * Channel row plus the action cluster. The "Ask" button from youtube.com is
 * deliberately absent: the AI Q&A block is out of scope (CLAUDE.md §7).
 */
export function VideoActions({ video, likeCount }: { video: Video; likeCount: number }) {
  const reaction = video.userState?.reaction ?? 'NONE'
  const subscribed = video.channel.subscribed

  const setReaction = useSetReaction(video.id)
  const setSubscription = useSetSubscription(video.channel.id)

  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="flex items-center gap-3">
        <Avatar hue={hueFromId(video.channel.id)} name={video.channel.name} size={40} />
        <div>
          <p className="flex items-center gap-1 text-base font-medium">
            {video.channel.name}
            {video.channel.verified && <CheckCircle size={14} className="text-text-2" />}
          </p>
          <p className="text-xs text-text-2">{formatSubscribers(video.channel.subscriberCount)}</p>
        </div>
        <button
          type="button"
          aria-pressed={subscribed}
          disabled={setSubscription.isPending}
          onClick={() => setSubscription.mutate(!subscribed)}
          className={
            subscribed
              ? 'ml-4 h-9 rounded-full bg-surface px-4 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover'
              : 'ml-4 h-9 rounded-full bg-invert-bg px-4 text-sm font-medium text-invert-text transition-colors duration-150 ease-out hover:bg-white'
          }
        >
          {subscribed ? 'Subscribed' : 'Subscribe'}
        </button>
      </div>

      <div className="flex items-center gap-2">
        <div className="flex h-9 items-center rounded-full bg-surface">
          <button
            type="button"
            aria-pressed={reaction === 'LIKE'}
            aria-label="Like"
            disabled={setReaction.isPending}
            onClick={() => setReaction.mutate(reaction === 'LIKE' ? 'NONE' : 'LIKE')}
            className="flex h-full items-center gap-2 rounded-l-full px-4 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            <ThumbsUp size={20} fill={reaction === 'LIKE' ? 'currentColor' : 'none'} />
            {formatCount(likeCount)}
          </button>
          <span className="h-6 w-px bg-line-subtle" />
          <button
            type="button"
            aria-pressed={reaction === 'DISLIKE'}
            aria-label="Dislike"
            disabled={setReaction.isPending}
            onClick={() => setReaction.mutate(reaction === 'DISLIKE' ? 'NONE' : 'DISLIKE')}
            className="grid h-full w-12 place-items-center rounded-r-full transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            <ThumbsDown size={20} fill={reaction === 'DISLIKE' ? 'currentColor' : 'none'} />
          </button>
        </div>

        <ActionPill
          icon={<Share2 size={20} />}
          label="Share"
          onClick={() => void navigator.clipboard?.writeText(window.location.href)}
        />
        <ActionPill icon={<Bookmark size={20} />} label="Save" />

        <button
          type="button"
          aria-label="More actions"
          className="grid h-9 w-9 place-items-center rounded-full bg-surface transition-colors duration-150 ease-out hover:bg-surface-hover"
        >
          <MoreHorizontal size={20} />
        </button>
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
