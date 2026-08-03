import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import {
  useDiscover,
  useFeed,
  usePopular,
  useStorage,
  useTopPlayed,
  useTopics,
} from '@/features/catalog/application/queries'
import { useHiddenVideos } from '@/features/catalog/application/hidden'
import { ChipBar } from '@/features/catalog/ui/ChipBar'
import { ExternalVideoCard } from '@/features/catalog/ui/ExternalVideoCard'
import { StorageBanner } from '@/features/catalog/ui/StorageBanner'
import { TopPlayedCard } from '@/features/catalog/ui/TopPlayedCard'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'

/**
 * Serves both "/" and "/topic/:name". A topic route is the same grid with the
 * filter preselected, so the two cannot drift apart.
 */
export function HomePage() {
  const { topicName } = useParams()
  const [selected, setSelected] = useState('All')
  const active = topicName ?? selected

  // A different topic is a different grid, so it starts at its own beginning.
  //
  // Keeping the scroll position across the change put the viewer somewhere in
  // the middle of a list they had not seen the top of — and, having scrolled
  // down through the previous topic, often past the end of the new one.
  useEffect(() => {
    window.scrollTo({ top: 0 })
  }, [active])

  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useFeed(active)
  const { data: topics } = useTopics()
  const { data: storage } = useStorage()
  const { data: topPlayed } = useTopPlayed(25)
  const { data: popular } = usePopular(12)

  // Every section reads the same list. It used to be the feed alone, edited
  // through its query cache, so hiding a card in "Popular with you" removed it
  // from a list it was not in and left it exactly where it was.
  const hidden = useHiddenVideos()
  const visible = <T extends { id: string }>(items: T[] | undefined) =>
    (items ?? []).filter((item) => !hidden.has(item.id))
  // When browsing a topic, also show what YouTube has for it. The library is
  // what the topics chose to bring in; YouTube search stretches past that.
  const isTopic = active !== 'All'
  const { data: youtubeVideos } = useDiscover(isTopic ? active : '', 6)

  // Both extra rows belong to the unfiltered home. Under a topic the page is
  // answering a narrower question, and a global mix would be beside the point.
  const showCollections = active === 'All'

  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  // "All" leads; the rest are topics that actually have videos, so a chip can
  // never produce an empty grid.
  const chips = ['All', ...(topics ?? []).map((t) => t.name)]

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      {storage && (
        <StorageBanner usedBytes={storage.usedBytes} budgetBytes={storage.budgetBytes} />
      )}

      {topicName ? (
        <>
          <h1 className="py-4 text-2xl font-bold">{topicName}</h1>
          {youtubeVideos && youtubeVideos.length > 0 && (
            <section className="mb-6">
              <h2 className="mb-3 text-lg font-medium">
                From YouTube &middot; {topicName}
              </h2>
              <div className="grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 ">
                {youtubeVideos.map((video) => (
                  <ExternalVideoCard key={video.id} video={video} />
                ))}
              </div>
              <hr className="mt-8 border-0 border-t border-line" />
            </section>
          )}
        </>
      ) : (
        <ChipBar categories={chips} active={active} onSelect={setSelected} />
      )}

      {isError ? (
        <p className="py-16 text-center text-text-2">
          Could not reach the library service. Is the gateway running?
        </p>
      ) : (
        <>
          {showCollections && visible(popular).length > 0 && (
            <section className="pt-3">
              <h2 className="mb-3 text-lg font-medium">Popular with you</h2>
              <div className="grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 ">
                {visible(popular).map((video) => (
                  <VideoCard key={video.id} video={video} />
                ))}
              </div>
              <hr className="mt-10 border-0 border-t border-line" />
            </section>
          )}

          <div className="grid grid-cols-1 gap-x-4 gap-y-10 pt-3 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 ">
            {/* The mix leads the grid: it is the one entry that is a list
                rather than a video, and it is what a returning viewer most
                often wants. */}
            {showCollections && visible(topPlayed).length > 1 && (
              <TopPlayedCard videos={visible(topPlayed)} />
            )}
            {isPending
              ? Array.from({ length: 8 }, (_, i) => <VideoCardSkeleton key={i} />)
              : visible(videos).map((video) => <VideoCard key={video.id} video={video} />)}
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
          Nothing here yet. Topics are scanned every 12 hours; use Refresh to scan now.
        </p>
      )}
    </div>
  )
}
