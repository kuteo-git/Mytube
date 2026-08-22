import { useSaved } from '@/features/catalog/application/queries'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'
import { useTranslation } from 'react-i18next'
import { PageHeading } from '@/shared/ui/PageHeading'

export function SavedPage() {
  const { t } = useTranslation()
  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useSaved()

  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <PageHeading>{t('pages.saved.title')}</PageHeading>

      {isError ? (
        <p className="py-16 text-center text-text-2">
          {t('empty.couldNotLoad', { what: t('empty.what_savedVideos') })}
        </p>
      ) : (
        <>
          <div className="grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
            {isPending
              ? Array.from({ length: 8 }, (_, i) => <VideoCardSkeleton key={i} />)
              : videos.map((video) => <VideoCard key={video.id} video={video} variant="saved" />)}
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
          {t('empty.saved')}
        </p>
      )}
    </div>
  )
}
