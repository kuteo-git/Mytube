import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { useChannel, useChannelVideos } from '@/features/catalog/application/queries'
import { ChannelHeader } from '@/features/catalog/ui/ChannelHeader'
import { ExternalVideoCard } from '@/features/catalog/ui/ExternalVideoCard'
import { VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { channelQueueSearch } from '@/features/watch/application/queue'
import { InfiniteList } from '@/shared/ui/InfiniteList'
import { Pill } from '@/shared/ui/primitives'

/**
 * A channel's own page. The video grid comes live from YouTube, paged as you
 * scroll, rather than from the local library — subscribing does not have to
 * wait for a scan before the channel is browsable, and a channel is never
 * capped at whatever a scan happened to bring in.
 */
export function ChannelPage() {
  const { channelId } = useParams()
  const [sortToken, setSortToken] = useState<string | undefined>(undefined)

  // A token belongs to one channel. Carrying it across a navigation would ask
  // YouTube to continue a listing for a channel nobody is looking at.
  useEffect(() => setSortToken(undefined), [channelId])

  const { data: channelPage, isPending: channelPending, isError: channelError } =
    useChannel(channelId)
  const {
    data,
    isPending: videosPending,
    isError: videosError,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  } = useChannelVideos(channelId, sortToken)

  const videos = data?.pages.flatMap((page) => page.videos) ?? []
  // Only the first page carries the chips; later pages are continuations.
  const sortOptions = data?.pages[0]?.sortOptions ?? []

  if (channelError) {
    return <p className="py-16 text-center text-text-2">Channel not found.</p>
  }

  return (
    <div className="mx-auto max-w-6xl px-4 pb-16 min-[700px]:px-6">
      {channelPending || !channelPage ? (
        <div className="mt-4 aspect-[6/1] w-full animate-pulse rounded-xl bg-surface" />
      ) : (
        <ChannelHeader channel={channelPage.channel} videoCount={channelPage.videoCount} />
      )}

      {/* Orderings come from YouTube, so this row is empty rather than wrong
          when a channel offers none — and never renders a control that would
          do nothing. */}
      {sortOptions.length > 1 && (
        <div
          role="group"
          aria-label="Sort videos"
          className="mt-6 flex flex-wrap gap-3 border-b border-line pb-4"
        >
          {sortOptions.map((option, index) => {
            // Nothing picked yet means the page loaded in whatever order the
            // channel offers first.
            const active = sortToken === undefined ? index === 0 : sortToken === option.token
            return (
              <Pill
                key={option.token}
                active={active}
                aria-pressed={active}
                // Each chip sends its own token, including the first.
                //
                // The first used to send undefined, on the reasoning that it is
                // "the default". But this row is rebuilt from whatever the last
                // response carried, so position one is only the default until
                // YouTube returns the orderings in another order — after which
                // the chip labelled Popular asks for the default listing and
                // the list does not change, which is what "sometimes clicking
                // does nothing" looked like. A token means one ordering wherever
                // it sits.
                onClick={() => setSortToken(option.token)}
              >
                {option.label}
              </Pill>
            )
          })}
        </div>
      )}

      {videosError ? (
        <p className="mt-8 text-sm text-text-2">Could not reach YouTube to list this channel.</p>
      ) : (
        <>
          <div className="mt-8 grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
            {videosPending
              ? Array.from({ length: 8 }, (_, i) => <VideoCardSkeleton key={i} />)
              : videos.map((video) => (
                  // Opening a video here starts the channel as a queue, in the
                  // order currently on screen — so "next" is the next video
                  // down the page rather than an unrelated recommendation.
                  <ExternalVideoCard
                    key={video.id}
                    video={video}
                    queueSearch={channelId ? channelQueueSearch(channelId, sortToken) : ''}
                  />
                ))}
          </div>

          <InfiniteList
            hasMore={Boolean(hasNextPage)}
            isLoading={isFetchingNextPage}
            onLoadMore={() => void fetchNextPage()}
          />

          {!videosPending && videos.length === 0 && (
            <p className="mt-8 text-sm text-text-2">This channel has no uploads.</p>
          )}
        </>
      )}
    </div>
  )
}
