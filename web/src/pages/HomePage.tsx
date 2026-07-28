import { useState } from 'react'
import { useCategories, useFeed, useStorage } from '@/features/catalog/application/queries'
import { ChipBar } from '@/features/catalog/ui/ChipBar'
import { StorageBanner } from '@/features/catalog/ui/StorageBanner'
import { VideoCard, VideoCardSkeleton } from '@/features/catalog/ui/VideoCard'

export function HomePage() {
  const [category, setCategory] = useState('All')
  const { data: feed, isPending, isError } = useFeed(category)
  const { data: categories } = useCategories()
  const { data: storage } = useStorage()

  // "All" is always first; the rest come from tags actually present in the
  // library, so a filter can never produce an empty grid.
  const chips = ['All', ...(categories ?? []).map((c) => c.name)]

  return (
    <div className="px-6 pb-16">
      {storage && (
        <StorageBanner usedBytes={storage.usedBytes} budgetBytes={storage.budgetBytes} />
      )}
      <ChipBar categories={chips} active={category} onSelect={setCategory} />

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
        <p className="py-16 text-center text-text-2">No videos in this category yet.</p>
      )}
    </div>
  )
}
