import { useParams } from 'react-router-dom'
import { useChannel, useChannelVideos } from '@/features/catalog/application/queries'
import { ChannelHeader } from '@/features/catalog/ui/ChannelHeader'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'

export function ChannelPage() {
  const { channelId } = useParams()
  const { data: channelPage, isPending: channelPending, isError: channelError } =
    useChannel(channelId)
  const { data, isPending: videosPending, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useChannelVideos(channelId)

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

      <div className="mt-8 grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
        {videosPending
          ? Array.from({ length: 8 }, (_, i) => <VideoCardSkeleton key={i} />)
          : videos.map((video) => <VideoCard key={video.id} video={video} />)}
      </div>

      <InfiniteList
        hasMore={Boolean(hasNextPage)}
        isLoading={isFetchingNextPage}
        onLoadMore={() => void fetchNextPage()}
      />

      {!videosPending && videos.length === 0 && (
        <p className="mt-8 text-sm text-text-2">
          No videos from this channel are in your library yet. Subscribing adds the
          channel as a source, and its uploads arrive on the next scan.
        </p>
      )}
    </div>
  )
}
