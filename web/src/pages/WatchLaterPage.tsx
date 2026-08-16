import { useWatchLater } from '@/features/catalog/application/queries'
import { useAccountState } from '@/features/settings/application/account-state'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'

/**
 * The videos somebody put aside to watch next.
 *
 * Deliberately separate from Saved, which the two were never the same thing:
 * Saved keeps a video's bytes on the disk against the eviction sweep, and
 * Watch Later is a note about what to do this evening. A video normally leaves
 * this list once it has been watched; it never leaves Saved on its own.
 */
export function WatchLaterPage() {
  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useWatchLater()
  const { signedOut } = useAccountState()

  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <h1 className="py-4 text-2xl font-bold">Watch later</h1>

      {isError ? (
        <p className="py-16 text-center text-text-2">
          Could not load Watch later. Is the gateway running?
        </p>
      ) : (
        <>
          <div className="grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
            {isPending
              ? Array.from({ length: 8 }, (_, i) => <VideoCardSkeleton key={i} />)
              : videos.map((video) => (
                  <VideoCard key={video.id} video={video} variant="fromYouTube" />
                ))}
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
          {signedOut
            ? 'YouTube signed you out, so this list is not being brought across. Paste your cookies again in Settings.'
            : 'Nothing here yet. This list is a copy of your YouTube Watch later, brought across on each account scan.'}
        </p>
      )}
    </div>
  )
}
