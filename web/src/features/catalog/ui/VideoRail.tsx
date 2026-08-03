import type { Video } from '@/features/catalog/domain/video'
import { VideoCard } from '@/features/catalog/ui/VideoCard'

/**
 * A row of videos that scrolls sideways instead of pushing the page down.
 *
 * A section above the feed competes with the feed for the first screen. As a
 * grid, twelve cards is four rows on a desktop and twelve on a phone, so
 * whatever it was offering arrived instead of the feed rather than before it.
 * A rail costs one row however many it holds.
 *
 * It bleeds to the screen edge below 700px, the same way ChipBar does and for
 * the same reason recorded in CLAUDE.md §8b: a scrolling row that stops short
 * of the edge reads as having ended rather than as continuing.
 */
export function VideoRail({ videos }: { videos: Video[] }) {
  if (videos.length === 0) return null

  return (
    <div
      // py-2, not pb-2. The cards carry a negative margin so their hover tint
      // has a body without costing layout, which means each one bleeds 8px past
      // its slot on every side — and overflow-x-auto clips vertically too, so
      // the tops were being cut off. The padding is the room that bleed needs,
      // and it has to be equal top and bottom or the row sits off-centre.
      className="-mx-4 flex snap-x snap-mandatory gap-4 overflow-x-auto px-4 py-2
                 no-scrollbar min-[700px]:mx-0 min-[700px]:px-0"
    >
      {videos.map((video) => (
        <div
          key={video.id}
          // A width, because a flex child of a scroller shrinks to its content
          // otherwise. Sized so the next card is always part-visible: a row cut
          // exactly at the edge looks like the end of the list.
          className="w-[70%] shrink-0 snap-start min-[560px]:w-[45%] min-[1000px]:w-[30%] min-[1400px]:w-[23%]"
        >
          <VideoCard video={video} />
        </div>
      ))}
    </div>
  )
}
