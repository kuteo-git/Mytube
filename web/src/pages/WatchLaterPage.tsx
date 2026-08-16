import { useWatchLater } from '@/features/catalog/application/queries'
import { useAccountState } from '@/features/settings/application/account-state'
import { watchLaterQueueSearch } from '@/features/watch/application/queue'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'

/**
 * The videos somebody put aside to watch next.
 *
 * Built to match PlaylistPage exactly — same heading and count, same grid, same
 * card variant, and the same behaviour when one is opened. The two are the same
 * kind of thing: a read-only list from the member's YouTube account, played
 * through in its own order. Any difference between them is a difference the
 * viewer has to learn for no reason.
 *
 * Deliberately separate from Saved, which is not the same kind of thing at all:
 * Saved keeps a video's bytes on the disk against the eviction sweep, and this
 * is a note about what to do this evening.
 */
export function WatchLaterPage() {
  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useWatchLater()
  const { signedOut } = useAccountState()

  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <h1 className="py-4 text-2xl font-bold">Watch later</h1>
      {!isPending && !isError && (
        <p className="-mt-2 pb-4 text-sm text-text-2">
          {videos.length} {videos.length === 1 ? 'video' : 'videos'}
        </p>
      )}

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
                  <VideoCard
                    key={video.id}
                    video={video}
                    variant="fromYouTube"
                    queueSearch={watchLaterQueueSearch()}
                  />
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
