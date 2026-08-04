import clsx from 'clsx'
import { useEffect, useRef, useState } from 'react'
import { useParams } from 'react-router-dom'
import {
  useDiscover,
  useFeed,
  useHistory,
  useStorage,
  useTopPlayed,
  useTopics,
} from '@/features/catalog/application/queries'
import { isInProgress } from '@/features/catalog/domain/video'
import { useHiddenVideos } from '@/features/catalog/application/hidden'
import { ChipBar } from '@/features/catalog/ui/ChipBar'
import { ExternalVideoCard } from '@/features/catalog/ui/ExternalVideoCard'
import { StorageBanner } from '@/features/catalog/ui/StorageBanner'
import { TopPlayedCard } from '@/features/catalog/ui/TopPlayedCard'
import { VideoRail } from '@/features/catalog/ui/VideoRail'
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
  //
  // Only on a *change*, which is what the sentence above says and what this
  // effect did not do: it fired on mount as well, and mounting is exactly what
  // happens when you come back to Home from another tab. So returning to Home
  // was thrown to the top a frame after being restored to where you left it,
  // and the scroll memory looked broken on the one page people scroll most.
  //
  // Chips are local state rather than navigation, so useScrollRestoration
  // cannot see this and the effect has to stay.
  const lastTopicRef = useRef<string | null>(null)
  useEffect(() => {
    const previous = lastTopicRef.current
    lastTopicRef.current = active
    if (previous === null || previous === active) return
    window.scrollTo({ top: 0 })
  }, [active])

  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useFeed(active)
  const { data: topics } = useTopics()
  const { data: storage } = useStorage()
  const { data: topPlayed } = useTopPlayed(25)
  // Partly watched, most recent first — history already comes back in that
  // order. isInProgress is the same 2%-to-95% window the feed ranks by, so a
  // video cannot be finished here and unfinished there.
  const { data: history } = useHistory()

  // Every section reads the same list. It used to be the feed alone, edited
  // through its query cache, so hiding a card in "Popular with you" removed it
  // from a list it was not in and left it exactly where it was.
  const hidden = useHiddenVideos()
  const visible = <T extends { id: string }>(items: T[] | undefined) =>
    (items ?? []).filter((item) => !hidden.has(item.id))

  // Filtered before the cut, not after, so marking one watched pulls the next
  // one up rather than leaving eleven cards and a gap.
  //
  // This row is the one that most needed it and was the one that did not have
  // it: "Watched" is a statement about a video you are part way through, so the
  // card it is pressed on is nearly always in this rail — and nothing here
  // removed it. The history query is not refetched by the mutation either, so
  // the card sat there, still half-watched, until the page was reloaded.
  const continueWatching = visible(history?.pages.flatMap((page) => page.videos))
    .filter(isInProgress)
    .slice(0, 12)
  // When browsing a topic, also show what YouTube has for it. The library is
  // what the topics chose to bring in; YouTube search stretches past that.
  const isTopic = active !== 'All'
  const { data: youtubeVideos } = useDiscover(isTopic ? active : '', 6)

  // Both extra rows belong to the unfiltered home. Under a topic the page is
  // answering a narrower question, and a global mix would be beside the point.
  const showCollections = active === 'All'
  const railShown = showCollections && continueWatching.length > 0

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
          {/* What a returning viewer came back for, and the one row that can
              be finished rather than browsed. A rail, not a grid: as twelve
              cards this section arrived instead of the feed rather than before
              it — four rows on a desktop and twelve on a phone. */}
          {railShown && (
            <section className="pt-3">
              <h2 className="mb-3 text-lg font-medium">Continue watching</h2>
              <VideoRail videos={continueWatching} />
              {/* Space below the rule belongs to the grid that follows, so it
                  is set there — matched to this margin. */}
              <hr className="mt-8 border-0 border-t border-line" />
            </section>
          )}

          <div
            className={clsx(
              'grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3',
              // Matched to the rule's own top margin so the divider sits
              // centred between the rail and the feed, rather than tucked
              // against the grid.
              railShown ? 'pt-8' : 'pt-3',
            )}
          >
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
