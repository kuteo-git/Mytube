import clsx from 'clsx'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'
import {
  useDiscover,
  useFeed,
  useHistory,
  useStorage,
  useTopPlayed,
  useLive,
  useTopics,
} from '@/features/catalog/application/queries'
import { isInProgress } from '@/features/catalog/domain/video'
import { useHiddenVideos } from '@/features/catalog/application/hidden'
import { ALL_CATEGORY, ChipBar, LIVE_CATEGORY } from '@/features/catalog/ui/ChipBar'
import { ExternalVideoCard } from '@/features/catalog/ui/ExternalVideoCard'
import { StorageBanner } from '@/features/catalog/ui/StorageBanner'
import { TopPlayedCard } from '@/features/catalog/ui/TopPlayedCard'
import { VideoRail } from '@/features/catalog/ui/VideoRail'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { usePlayer } from '@/features/watch/application/player-context'
import { PullIndicator } from '@/features/catalog/ui/PullIndicator'
import { usePullToRefresh } from '@/features/catalog/application/use-pull-to-refresh'
import { InfiniteList } from '@/shared/ui/InfiniteList'
import { Trans, useTranslation } from 'react-i18next'

/**
 * Serves both "/" and "/topic/:name". A topic route is the same grid with the
 * filter preselected, so the two cannot drift apart.
 */
export function HomePage() {
  const { t } = useTranslation()
  const { topicName } = useParams()
  const [selected, setSelected] = useState<string>(ALL_CATEGORY)
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
  const { scrollerEl, isMobile } = usePlayer()

  /**
   * Pull the feed down to fetch it again. A phone has no refresh button and
   * should not grow one — the gesture is the control everywhere else.
   *
   * It answers a real question here rather than being a flourish: the scanner
   * runs hourly and the ranking is frozen into a thirty-minute snapshot
   * (CLAUDE.md §8b), so "is there anything new" is something the page will
   * otherwise not go and ask.
   *
   * Everything the page is made of, not just the grid. Refreshing the feed and
   * leaving Continue watching and Popular with you as they were would be a page
   * that is half new — and the half that did not move is the half the viewer
   * would notice.
   */
  const queryClient = useQueryClient()
  const refresh = useCallback(
    () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: ['feed'] }),
        queryClient.invalidateQueries({ queryKey: ['history'] }),
        queryClient.invalidateQueries({ queryKey: ['top-played'] }),
        queryClient.invalidateQueries({ queryKey: ['storage'] }),
      ]),
    [queryClient],
  )
  const pull = usePullToRefresh({
    scroller: scrollerEl,
    // Touch only, and Home only. On a desktop the page has a keyboard and a
    // reload button; a drag there would be a second way to do something that
    // already has two.
    enabled: isMobile,
    onRefresh: refresh,
  })
  const lastTopicRef = useRef<string | null>(null)
  useEffect(() => {
    const previous = lastTopicRef.current
    lastTopicRef.current = active
    if (previous === null || previous === active) return
    // The page scrolls inside <main>, not the window — see AppShell.
    scrollerEl?.scrollTo({ top: 0 })
  }, [active, scrollerEl])

  const { data, isPending, isError, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useFeed(active)
  const { data: live } = useLive()
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
  const isTopic = active !== 'All' && active !== LIVE_CATEGORY
  const { data: youtubeVideos } = useDiscover(isTopic ? active : '', 6)

  // Both extra rows belong to the unfiltered home. Under a topic the page is
  // answering a narrower question, and a global mix would be beside the point.
  const showCollections = active === 'All'
  const railShown = showCollections && continueWatching.length > 0

  const videos = data?.pages.flatMap((page) => page.videos) ?? []

  // "All" leads; the rest are topics that actually have videos, so a chip can
  // never produce an empty grid.
  //
  // Live sits second, and only while something is actually on air. A chip that
  // is always there and usually empty would be worse than none: a red dot is a
  // claim that something is happening, and one that is lit over an empty grid
  // teaches people to stop believing it.
  const chips = [
    ALL_CATEGORY,
    ...(live && live.length > 0 ? [LIVE_CATEGORY] : []),
    ...(topics ?? []).map((t) => t.name),
  ]

  // The Live chip shows a list, not a ranking — so it does not go through the
  // feed query at all. Nothing here is scored, sampled or diversity-capped:
  // "everything on air" is a promise the ranker has no way to keep.
  const showingLive = active === LIVE_CATEGORY

  return (
    // `relative` so the indicator has something to be positioned against, and
    // the pull moves the content rather than the scroller: the scroller is
    // shared with every other page, and translating it would leave whatever
    // came next displaced.
    <div
      className="relative px-4 pb-16 min-[700px]:px-6"
      style={{
        transform: pull.offset > 0 ? `translateY(${pull.offset}px)` : undefined,
        // Follows the finger with no transition, springs back with one. A
        // transition during the drag puts the page a frame behind the hand.
        // Follows the finger with no transition, and animates on the way back —
        // both when a short pull is abandoned and when the refresh finishes and
        // the page closes over the spinner.
        transition: pull.pulling ? undefined : 'transform 200ms ease-out',
      }}
    >
      <PullIndicator distance={pull.distance} refreshing={pull.refreshing} />
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
          {t('empty.couldNotReachLibrary')}
        </p>
      ) : (
        <>
          {/* What a returning viewer came back for, and the one row that can
              be finished rather than browsed. A rail, not a grid: as twelve
              cards this section arrived instead of the feed rather than before
              it — four rows on a desktop and twelve on a phone. */}
          {railShown && (
            <section className="pt-3">
              <h2 className="mb-3 text-lg font-medium">{t('pages.home.continueWatching')}</h2>
              <VideoRail videos={continueWatching} variant="continueWatching" />
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
            {showingLive ? (
              (live ?? []).map((video) => <VideoCard key={video.id} video={video} />)
            ) : isPending ? (
              Array.from({ length: 8 }, (_, i) => <VideoCardSkeleton key={i} />)
            ) : (
              visible(videos).map((video) => <VideoCard key={video.id} video={video} />)
            )}
          </div>

          <InfiniteList
            hasMore={Boolean(hasNextPage)}
            isLoading={isFetchingNextPage}
            onLoadMore={() => void fetchNextPage()}
          />

          {/* The feed can now genuinely run out. A share set to 0% removes those
              videos from Home entirely rather than parking them at the end, so a
              viewer who follows a few channels reaches the bottom in a page or
              two — and without a word here that reads as the page having broken.
              It names the setting that decides it, because that is the only
              thing that will change the answer. */}
          {!isPending && !hasNextPage && videos.length > 0 && (
            <p className="py-10 text-center text-sm text-text-2">
              <Trans
                i18nKey="more.everythingShown"
                components={[
                  <Link key="l" to="/settings/feed" className="underline hover:text-text" />,
                ]}
              />
            </p>
          )}
        </>
      )}

      {!isPending && !isError && videos.length === 0 && (
        <p className="py-16 text-center text-text-2">
          <Trans
            i18nKey="more.nothingYetHome"
            components={[
              <Link key="l" to="/settings/feed" className="underline hover:text-text" />,
            ]}
          />
        </p>
      )}
    </div>
  )
}
