import { useWatchLater } from '@/features/catalog/application/queries'
import { useAccountState } from '@/features/settings/application/account-state'
import { watchLaterQueueSearch } from '@/features/watch/application/queue'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'
import { useTranslation } from 'react-i18next'
import { PageHeading } from '@/shared/ui/PageHeading'

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
  const { t } = useTranslation()
  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useWatchLater()
  const { signedOut } = useAccountState()

  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <PageHeading>{t('pages.watchLater.title')}</PageHeading>
      {/* No negative margin. It existed to pull this up under the heading's own
          padding, and with the heading gone it pulled the first line of the page
          up underneath the back bar instead — the bar is an overlay and reserves
          nothing, so anything with a negative top margin lands behind it. */}
      {!isPending && !isError && (
        <p className="py-4 text-sm text-text-2">
          {t('more.videosCount', { count: videos.length })}
        </p>
      )}

      {isError ? (
        <p className="py-16 text-center text-text-2">
          {t('empty.couldNotLoad', { what: t('empty.what_watchLater') })}
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
            ? t('pages.watchLater.signedOut')
            : t('pages.watchLater.empty')}
        </p>
      )}
    </div>
  )
}
