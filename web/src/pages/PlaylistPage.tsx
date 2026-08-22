import { useParams } from 'react-router-dom'
import { usePlaylist } from '@/features/catalog/application/queries'
import { playlistQueueSearch } from '@/features/watch/application/queue'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'
import { useTranslation } from 'react-i18next'
import { BackBar } from '@/features/catalog/ui/BackBar'
import { usePlayer } from '@/features/watch/application/player-context'
import { PageHeading } from '@/shared/ui/PageHeading'

/**
 * One playlist, in its own order.
 *
 * The order is the playlist's `position`, filled from the order an import
 * returned and appended to at the end. A list of music that comes back sorted
 * by date is a different list.
 */
export function PlaylistPage() {
  const { isMobile } = usePlayer()
  const { t } = useTranslation()
  const { playlistId = '' } = useParams()
  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    usePlaylist(playlistId)

  const playlist = data?.pages[0]?.playlist
  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      {/* Its own bar, like a channel's: the name is the playlist's and the
          shell cannot know it, so `bare-screens` records a null title here and
          leaves the naming to this page. */}
      {isMobile && (
        <BackBar
          title={playlist?.title ?? t('ui.playlist')}
          showTitle
          fallback="/playlists"
        />
      )}
      <PageHeading>{playlist?.title ?? t('ui.playlist')}</PageHeading>
      {playlist && (
        <p className="pb-4 text-sm text-text-2 min-[700px]:-mt-2">
          {playlist.unavailable
            ? t('pages.playlists.wontOpen')
            : playlist.itemsSynced
              ? t('more.videosCount', { count: playlist.itemCount })
              : t('pages.playlists.notReadYet')}
        </p>
      )}

      {isError ? (
        <p className="py-16 text-center text-text-2">
          {t('more.playlistUnopenable')}
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
                    // Opening a video here plays the playlist, in the
                    // playlist's order: next is the next entry rather than a
                    // recommendation. Without it a playlist is only a way of
                    // finding something to leave it by.
                    queueSearch={playlistQueueSearch(playlistId)}
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
          {playlist?.unavailable
            ? t('pages.playlists.wontOpenLong')
            : playlist && !playlist.itemsSynced
              ? t('pages.playlists.notReadYetLong')
              : t('pages.playlists.emptyUpstream')}
        </p>
      )}
    </div>
  )
}
