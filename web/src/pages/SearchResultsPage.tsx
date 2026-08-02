import { Loader2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useDiscover, useSearch } from '@/features/catalog/application/queries'
import { ExternalVideoCard } from '@/features/catalog/ui/ExternalVideoCard'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'
import { InfiniteList } from '@/shared/ui/InfiniteList'

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

export function SearchResultsPage() {
  const [params] = useSearchParams()
  const query = params.get('q') ?? ''

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

  // Videos already in the library appear in the first section; showing them
  // twice would just be noise.
  const localIds = new Set(local.map((v) => v.id))
  const remaining = (upstream ?? []).filter((v) => !localIds.has(v.id))

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <h1 className="py-6 text-xl">
        Results for <span className="font-bold">{query}</span>
      </h1>

      <section>
        <h2 className="mb-4 text-base font-medium text-text-2">In your library</h2>
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
          <p className="text-sm text-text-2">Nothing here matches.</p>
        )}
      </section>

      <section className="mt-12">
        <h2 className="mb-4 flex items-center gap-2 text-base font-medium text-text-2">
          On YouTube
          {upstreamPending && <Loader2 size={16} className="animate-spin" />}
        </h2>

        {upstreamError ? (
          <p className="text-sm text-text-2">Could not reach YouTube.</p>
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
          <p className="text-sm text-text-2">No further results.</p>
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
