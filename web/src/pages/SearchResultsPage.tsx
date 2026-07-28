import { useSearchParams } from 'react-router-dom'
import { useSearch } from '@/features/catalog/application/queries'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'

/**
 * Full-text results over the local library. Search never reaches YouTube: the
 * only videos this app can play are the ones its topics brought in.
 */
export function SearchResultsPage() {
  const [params] = useSearchParams()
  const query = params.get('q') ?? ''
  const { data: videos, isPending, isError } = useSearch(query)

  return (
    <div className="px-6 pb-16">
      <h1 className="py-6 text-xl">
        Results for <span className="font-bold">{query}</span>
      </h1>

      {isError ? (
        <p className="py-16 text-center text-text-2">Search failed. Is the gateway running?</p>
      ) : (
        <div className="grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
          {isPending
            ? Array.from({ length: 4 }, (_, i) => <VideoCardSkeleton key={i} />)
            : videos?.map((video) => <VideoCard key={video.id} video={video} />)}
        </div>
      )}

      {!isPending && !isError && videos?.length === 0 && (
        <p className="py-16 text-center text-text-2">
          Nothing in the library matches that. Only videos brought in by your topics can be found
          here.
        </p>
      )}
    </div>
  )
}
