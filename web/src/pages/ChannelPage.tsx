import { useParams } from 'react-router-dom'
import { useChannel, useChannelVideos } from '@/features/catalog/application/queries'
import { ChannelHeader } from '@/features/catalog/ui/ChannelHeader'
import { ExternalVideoCard } from '@/features/catalog/ui/ExternalVideoCard'
import { VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'

/**
 * A channel's own page. The video grid comes live from YouTube, paged as you
 * scroll, rather than from the local library — subscribing does not have to
 * wait for a scan before the channel is browsable, and a channel is never
 * capped at whatever a scan happened to bring in.
 */
export function ChannelPage() {
  const { channelId } = useParams()
  const { data: channelPage, isPending: channelPending, isError: channelError } =
    useChannel(channelId)
  const {
    data,
    isPending: videosPending,
    isError: videosError,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  } = useChannelVideos(channelId)

  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  if (channelError) {
    return <p className="py-16 text-center text-text-2">Channel not found.</p>
  }

  return (
    <div className="mx-auto max-w-6xl px-6 pb-16">
      {channelPending || !channelPage ? (
        <div className="mt-4 aspect-[6/1] w-full animate-pulse rounded-xl bg-surface" />
      ) : (
        <ChannelHeader channel={channelPage.channel} videoCount={channelPage.videoCount} />
      )}

      {videosError ? (
        <p className="mt-8 text-sm text-text-2">Could not reach YouTube to list this channel.</p>
      ) : (
        <>
          <div className="mt-8 grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
            {videosPending
              ? Array.from({ length: 8 }, (_, i) => <VideoCardSkeleton key={i} />)
              : videos.map((video) => <ExternalVideoCard key={video.id} video={video} />)}
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
