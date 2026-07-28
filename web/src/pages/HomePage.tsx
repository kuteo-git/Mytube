import { useState } from 'react'
import { useParams } from 'react-router-dom'
import { useFeed, useStorage, useTopics } from '@/features/catalog/application/queries'
import { ChipBar } from '@/features/catalog/ui/ChipBar'
import { StorageBanner } from '@/features/catalog/ui/StorageBanner'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'

/**
 * Serves both "/" and "/topic/:name". A topic route is the same grid with the
 * filter preselected, so the two cannot drift apart.
 */
export function HomePage() {
  const { topicName } = useParams()
  const [selected, setSelected] = useState('All')
  const active = topicName ?? selected

  const { data: feed, isPending, isError } = useFeed(active)
  const { data: topics } = useTopics()
  const { data: storage } = useStorage()

  // "All" leads; the rest are topics that actually have videos, so a chip can
  // never produce an empty grid.
  const chips = ['All', ...(topics ?? []).map((t) => t.name)]

  return (
    <div className="px-6 pb-16">
      {storage && (
        <StorageBanner usedBytes={storage.usedBytes} budgetBytes={storage.budgetBytes} />
      )}

      {topicName ? (
        <h1 className="py-4 text-2xl font-bold">{topicName}</h1>
      ) : (
        <ChipBar categories={chips} active={active} onSelect={setSelected} />
      )}

      {isError ? (
        <p className="py-16 text-center text-text-2">
          Could not reach the library service. Is the gateway running?
        </p>
      ) : (
        <div className="grid grid-cols-1 gap-x-4 gap-y-10 pt-3 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
          {isPending
            ? Array.from({ length: 8 }, (_, i) => <VideoCardSkeleton key={i} />)
            : feed?.videos.map((video) => <VideoCard key={video.id} video={video} />)}
        </div>
      )}

      {!isPending && !isError && feed?.videos.length === 0 && (
        <p className="py-16 text-center text-text-2">
          Nothing here yet. Topics are scanned every 12 hours; use Refresh to scan now.
        </p>
      )}
    </div>
  )
}
