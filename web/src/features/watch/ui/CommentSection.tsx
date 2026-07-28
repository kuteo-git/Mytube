import { ChevronDown, ListFilter, Pin, ThumbsDown, ThumbsUp } from 'lucide-react'
import { useState } from 'react'
import type { Comment } from '@/features/catalog/domain/video'
import { useAddComment, useComments } from '@/features/catalog/application/queries'
import { Avatar } from '@/shared/ui/primitives'
import { formatCount, formatRelative } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'

export function CommentSection({ videoId }: { videoId: string }) {
  const { data } = useComments(videoId)
  const addComment = useAddComment(videoId)
  const [draft, setDraft] = useState('')

  return (
    <section className="mt-6" aria-label="Comments">
      <div className="flex items-center gap-8">
        <h2 className="text-xl font-bold">{data?.totalCount ?? 0} Comments</h2>
        <button
          type="button"
          className="flex items-center gap-2 text-sm font-medium hover:text-text-2"
        >
          <ListFilter size={20} />
          Sort by
        </button>
      </div>

      <form
        className="mt-6 flex gap-3"
        onSubmit={(e) => {
          e.preventDefault()
          const text = draft.trim()
          if (!text) return
          addComment.mutate(text, { onSuccess: () => setDraft('') })
        }}
      >
        <Avatar hue={hueFromId('u_luc')} name="Luc" size={40} />
        <input
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="Add a comment..."
          aria-label="Add a comment"
          className="flex-1 border-b border-line bg-transparent pb-1 text-sm outline-none placeholder:text-text-2 focus:border-text"
        />
      </form>

      <ul className="mt-6 flex flex-col gap-5">
        {data?.comments.map((comment) => (
          <li key={comment.id}>
            <CommentThread comment={comment} />
          </li>
        ))}
      </ul>
    </section>
  )
}

function CommentThread({ comment }: { comment: Comment }) {
  const [showReplies, setShowReplies] = useState(false)
  const replies = comment.replies ?? []

  return (
    <article className="flex gap-3">
      <Avatar
        hue={hueFromId(comment.authorHandle)}
        name={comment.authorHandle.replace('@', '')}
        size={40}
      />

      <div className="min-w-0 flex-1">
        {comment.pinnedBy && (
          <p className="mb-1 flex items-center gap-1.5 text-xs text-text-2">
            <Pin size={14} /> Pinned by {comment.pinnedBy}
          </p>
        )}
        <p className="flex items-center gap-2 text-[13px] font-medium">
          {comment.authorHandle}
          <span className="text-xs font-normal text-text-2">
            {formatRelative(comment.publishedAt)}
          </span>
        </p>
        <p className="mt-1 text-sm whitespace-pre-line">{comment.text}</p>

        <div className="mt-1.5 flex items-center gap-2 text-text-2">
          <button
            type="button"
            aria-label="Like comment"
            className="grid h-8 w-8 place-items-center rounded-full hover:bg-surface-hover"
          >
            <ThumbsUp size={16} />
          </button>
          <span className="text-xs">{formatCount(comment.likeCount)}</span>
          <button
            type="button"
            aria-label="Dislike comment"
            className="grid h-8 w-8 place-items-center rounded-full hover:bg-surface-hover"
          >
            <ThumbsDown size={16} />
          </button>
          <button
            type="button"
            className="ml-2 rounded-full px-3 py-1.5 text-xs font-medium hover:bg-surface-hover"
          >
            Reply
          </button>
        </div>

        {replies.length > 0 && (
          <>
            <button
              type="button"
              aria-expanded={showReplies}
              onClick={() => setShowReplies((s) => !s)}
              className="mt-1 flex items-center gap-2 rounded-full px-3 py-1.5 text-sm font-medium text-link hover:bg-link/10"
            >
              <ChevronDown
                size={18}
                className="transition-transform duration-150 ease-out"
                style={{ transform: showReplies ? 'rotate(180deg)' : undefined }}
              />
              {replies.length} {replies.length === 1 ? 'reply' : 'replies'}
            </button>

            {showReplies && (
              <ul className="mt-3 flex flex-col gap-4">
                {replies.map((reply) => (
                  <li key={reply.id}>
                    <CommentThread comment={reply} />
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </div>
    </article>
  )
}
