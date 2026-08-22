import { ChevronDown, ListFilter, Pin, ThumbsDown, ThumbsUp, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import type { Comment } from '@/features/catalog/domain/video'
import { useAddComment, useComments, useFetchComments } from '@/features/catalog/application/queries'
import { Avatar } from '@/shared/ui/primitives'
import { InfiniteList } from '@/shared/ui/InfiniteList'

import { hueFromId } from '@/shared/lib/hue'
import { useFormat } from '@/shared/lib/useFormat'
import { useTranslation } from 'react-i18next'

export function CommentSection({ videoId }: { videoId: string }) {
  const { t } = useTranslation()
  const { data, isPending, fetchNextPage, hasNextPage, isFetchingNextPage } = useComments(videoId)
  const addComment = useAddComment(videoId)
  const fetchComments = useFetchComments(videoId)
  const [draft, setDraft] = useState('')
  const [autoFetched, setAutoFetched] = useState(false)

  const handleLoadMore = useCallback(() => {
    void fetchNextPage()
  }, [fetchNextPage])

  // Auto-fetch YouTube comments when a video has none.
  useEffect(() => {
    if (isPending || autoFetched || fetchComments.isPending) return
    if (data && data.totalCount === 0) {
      setAutoFetched(true)
      fetchComments.mutate()
    }
  }, [data?.totalCount, isPending, autoFetched])

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
          placeholder={t('comments.placeholder')}
          aria-label={t('comments.label')}
          className="flex-1 border-b border-line bg-transparent pb-1 text-sm outline-none placeholder:text-text-2 focus:border-text"
        />
      </form>

      {/* Skeleton shimmer while fetching YouTube comments */}
      {fetchComments.isPending && (
        <div className="mt-6 flex flex-col gap-5">
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="flex gap-3">
              <div className="h-10 w-10 shrink-0 animate-pulse rounded-full bg-surface" />
              <div className="flex-1 space-y-2">
                <div className="h-3 w-24 animate-pulse rounded bg-surface" />
                <div className="h-3 w-full animate-pulse rounded bg-surface" />
                <div className="h-3 w-2/3 animate-pulse rounded bg-surface" />
              </div>
            </div>
          ))}
        </div>
      )}

      {/*
        Nothing came back, and the two reasons read the same to a viewer.

        `isError` is the request itself going wrong; `unavailable` is YouTube
        declining to answer, which arrives under a 200 because nothing here is
        broken — it used to be a 500, and a page where the video played
        perfectly well reported an internal server error. Both offer the same
        Retry, because both are usually over by the next press.
      */}
      {(fetchComments.isError || fetchComments.data?.unavailable) && (
        <div className="mt-6 flex flex-col items-start gap-3 rounded-lg bg-surface p-4">
          <p className="text-sm text-text-2">
            {fetchComments.data?.unavailable
              ? t('comments.noneReturned')
              : t('comments.couldNotLoad')}
          </p>
          <button
            type="button"
            onClick={() => fetchComments.mutate()}
            className="flex items-center gap-2 rounded-lg bg-surface-hover px-4 py-2 text-sm font-medium transition-colors hover:bg-white/15"
          >
            <RefreshCw size={14} />
            Retry
          </button>
        </div>
      )}

      {!fetchComments.isPending && (
        <ul className="mt-6 flex flex-col gap-5">
          {data?.comments.map((comment) => (
            <li key={comment.id}>
              <CommentThread comment={comment} />
            </li>
          ))}
        </ul>
      )}

      <InfiniteList
        hasMore={Boolean(hasNextPage)}
        isLoading={isFetchingNextPage}
        onLoadMore={handleLoadMore}
      />
    </section>
  )
}

function CommentThread({ comment }: { comment: Comment }) {
  const { t } = useTranslation()
  const fmt = useFormat()
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
            {fmt.relative(comment.publishedAt)}
          </span>
        </p>
        <p className="mt-1 text-sm whitespace-pre-line">{comment.text}</p>

        <div className="mt-1.5 flex items-center gap-2 text-text-2">
          <button
            type="button"
            aria-label={t('comments.like')}
            className="grid h-8 w-8 place-items-center rounded-full hover:bg-surface-hover"
          >
            <ThumbsUp size={16} />
          </button>
          <span className="text-xs">{fmt.count(comment.likeCount)}</span>
          <button
            type="button"
            aria-label={t('comments.dislike')}
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
