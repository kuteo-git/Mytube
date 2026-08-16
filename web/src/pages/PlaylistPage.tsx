import { useParams } from 'react-router-dom'
import { usePlaylist } from '@/features/catalog/application/queries'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'

/**
 * One playlist, in its own order.
 *
 * The order is the playlist's `position`, filled from the order an import
 * returned and appended to at the end. A list of music that comes back sorted
 * by date is a different list.
 */
export function PlaylistPage() {
  const { playlistId = '' } = useParams()
  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    usePlaylist(playlistId)

  const playlist = data?.pages[0]?.playlist
  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <h1 className="py-4 text-2xl font-bold">{playlist?.title ?? 'Playlist'}</h1>
      {playlist && (
        <p className="-mt-2 pb-4 text-sm text-text-2">
          {playlist.itemCount} {playlist.itemCount === 1 ? 'video' : 'videos'}
        </p>
      )}

      {isError ? (
        <p className="py-16 text-center text-text-2">
          That playlist could not be opened. It may have been deleted, or it belongs to
          another profile.
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
                    variant="playlist"
                    playlistId={playlistId}
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
          Nothing in this playlist yet. Add videos from any video&rsquo;s menu.
        </p>
      )}
    </div>
  )
}
