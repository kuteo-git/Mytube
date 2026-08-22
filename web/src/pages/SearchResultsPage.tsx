import { Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useChannelLink, useDiscover, useSearch } from '@/features/catalog/application/queries'
import { ExternalVideoCard } from '@/features/catalog/ui/ExternalVideoCard'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'
import { useTranslation } from 'react-i18next'

/**
 * Two sections, deliberately.
 *
 * Topics decide what the feed offers; search always reaches YouTube as well,
 * because looking for something is exactly the case the topics were never
 * going to cover. What stays separate is whether a result is already on disk:
 * one plays in two seconds, the other has to be fetched first, and hiding that
 * difference would take away the thing worth knowing before clicking.
 */
const UPSTREAM_PAGE = 20

/**
 * Whether the query is an address rather than a question — for the heading only.
 *
 * Deliberately cruder than the gateway's parser, and not a second copy of it.
 * Which video an address names is one rule and lives in one place (§4); this
 * only asks whether to print the string back at the viewer, and getting that
 * wrong costs a clumsy heading rather than a wrong result.
 */
function looksLikeLink(query: string): boolean {
  return /^(https?:\/\/|vnd\.youtube|www\.|youtu\.be\/|m\.youtube\.)/i.test(query.trim())
}

export function SearchResultsPage() {
  const { t } = useTranslation()
  const [params] = useSearchParams()
  const query = params.get('q') ?? ''
  const navigate = useNavigate()

  // A pasted channel address is not a search, so it does not stay on this page.
  //
  // It used to: the query went to `ytsearch20:<the URL>`, which spends a
  // counted upstream request hunting for the text of an address, and the page
  // then said "Channel and playlist links cannot be opened here yet" — true,
  // and the whole of what happened.
  //
  // `replace`, so the back button leaves the channel for wherever the viewer
  // was rather than returning them to a search page that only ever redirects.
  const { data: channelLink } = useChannelLink(query)
  useEffect(() => {
    if (channelLink) navigate(`/channel/${channelLink}`, { replace: true })
  }, [channelLink, navigate])

  const {
    data: localPages,
    isPending: localPending,
    hasNextPage,
    isFetchingNextPage,
    fetchNextPage,
  } = useSearch(query)

  // Upstream search has no cursor, so "more" means requesting a larger page.
  const [upstreamLimit, setUpstreamLimit] = useState(UPSTREAM_PAGE)
  useEffect(() => setUpstreamLimit(UPSTREAM_PAGE), [query])

  const {
    data: upstream,
    isPending: upstreamPending,
    isFetching: upstreamFetching,
    isError: upstreamError,
  } = useDiscover(query, upstreamLimit)

  const local = localPages?.pages.flatMap((page) => page.videos) ?? []

  // A pasted address has exactly one right answer, so it shows in exactly one
  // place: the library if the video is here, YouTube if it is not. The other
  // section would be a heading with nothing under it, which reads as a section
  // still loading rather than one that was never going to have anything.
  const isLink = looksLikeLink(query)
  const settled = !localPending && !upstreamPending

  // Videos already in the library appear in the first section; showing them
  // twice would just be noise.
  const localIds = new Set(local.map((v) => v.id))
  const remaining = (upstream ?? []).filter((v) => !localIds.has(v.id))

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <h1 className="py-6 text-xl">
        {looksLikeLink(query) ? (
          // An address is not worth repeating back: it is long, it wraps, and
          // it says nothing the person who pasted it does not already know.
          // What it means is the interesting part.
          t('pages.search.fromLink')
        ) : (
          <>
            Results for <span className="font-bold">{query}</span>
          </>
        )}
      </h1>

      {isLink && settled && local.length === 0 && remaining.length === 0 && (
        <p className="text-sm text-text-2">
          That link does not lead to a video or a channel. Playlist links cannot be opened here
          yet.
        </p>
      )}

      <section hidden={isLink && settled && local.length === 0}>
        <h2 className="mb-4 text-base font-medium text-text-2">{t('pages.search.inLibrary')}</h2>
        {localPending ? (
          <Grid>
            {Array.from({ length: 4 }, (_, i) => (
              <VideoCardSkeleton key={i} />
            ))}
          </Grid>
        ) : local.length > 0 ? (
          <>
            <Grid>
              {local.map((video) => (
                <VideoCard key={video.id} video={video} />
              ))}
            </Grid>
            <InfiniteList
              hasMore={Boolean(hasNextPage)}
              isLoading={isFetchingNextPage}
              onLoadMore={() => void fetchNextPage()}
            />
          </>
        ) : (
          <p className="text-sm text-text-2">{t('pages.search.noMatches')}</p>
        )}
      </section>

      <section className="mt-12" hidden={isLink && settled && remaining.length === 0}>
        <h2 className="mb-4 flex items-center gap-2 text-base font-medium text-text-2">
          On YouTube
          {upstreamPending && <Loader2 size={16} className="animate-spin" />}
        </h2>

        {upstreamError ? (
          <p className="text-sm text-text-2">{t('pages.search.youtubeUnreachable')}</p>
        ) : (
          <>
            <Grid>
              {remaining.map((video) => (
                <ExternalVideoCard key={video.id} video={video} />
              ))}
            </Grid>
            {remaining.length > 0 && (
              <InfiniteList
                // Upstream results run out eventually; asking for a larger page
                // than the last one returned means there is nothing left.
                hasMore={(upstream?.length ?? 0) >= upstreamLimit}
                isLoading={upstreamFetching}
                onLoadMore={() => setUpstreamLimit((n) => n + UPSTREAM_PAGE)}
              />
            )}
          </>
        )}

        {!upstreamPending && !upstreamError && remaining.length === 0 && (
          <p className="text-sm text-text-2">{t('pages.search.noMore')}</p>
        )}
      </section>
    </div>
  )
}

function Grid({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
      {children}
    </div>
  )
}
