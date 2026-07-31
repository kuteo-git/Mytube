import { useSaved } from '@/features/catalog/application/queries'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'

export function SavedPage() {
  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useSaved()

  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  return (
    <div className="px-6 pb-16">
      <h1 className="py-4 text-2xl font-bold">Saved videos</h1>

      {isError ? (
        <p className="py-16 text-center text-text-2">
          Could not load saved videos. Is the gateway running?
        </p>
      ) : (
        <>
          <div className="grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
            {isPending
              ? Array.from({ length: 8 }, (_, i) => <VideoCardSkeleton key={i} />)
              : videos.map((video) => <VideoCard key={video.id} video={video} />)}
          </div>

          <InfiniteList
            hasMore={Boolean(hasNextPage)}
            isLoading={isFetchingNextPage}
            onLoadMore={() => void fetchNextPage()}
          />
        </>
      )}

      {!isPending && !isError && videos.length === 0 && (
        <p className="py-16 text-center text-text-2">
          No saved videos yet. Keep a video from its menu or the Storage page.
        </p>
      )}
    </div>
  )
}
