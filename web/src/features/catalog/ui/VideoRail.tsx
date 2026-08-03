import type { Video } from '@/features/catalog/domain/video'
import { VideoCard } from '@/features/catalog/ui/VideoCard'

/**
 * A row of videos that scrolls sideways instead of pushing the page down.
 *
 * A section above the feed competes with it for the first screen. As a grid,
 * twelve cards is four rows on a desktop and twelve on a phone, so whatever it
 * offered arrived instead of the feed rather than before it. A rail costs one
 * row however many it holds.
 *
 * The cards are `flush` here and the slots carry the padding. Grid cards hang
 * 8px outside their slots so the hover tint has a body without costing layout,
 * which is free in a grid and a problem in anything that scrolls: a scroller
 * clips what overhangs it, and padding the scroller to buy that back does not
 * work — three goes at the right number ended with the first card still looking
 * sliced the moment it lit up. Padding the slot leaves nothing to clip.
 */
export function VideoRail({ videos }: { videos: Video[] }) {
  if (videos.length === 0) return null

  return (
    <div
      // Bleeds to the screen edge and pads its content back to the page gutter,
      // the way ChipBar does — a scrolling row that stops short of the edge
      // reads as having ended rather than as continuing (CLAUDE.md §8b). The
      // gutter is 16px below 700px and 24px above it, matching the page.
      //
      // -my-2 pays back the slots' vertical padding, so the row occupies the
      // same height it would have as a plain list of cards.
      className="-mx-4 -my-2 flex snap-x snap-mandatory gap-2 overflow-x-auto px-4 py-2
                 no-scrollbar min-[700px]:-mx-6 min-[700px]:px-6"
    >
      {videos.map((video) => (
        <div
          key={video.id}
          // A width, because a flex child of a scroller shrinks to its content
          // otherwise. Sized so the next card is always part-visible: a row cut
          // exactly at the edge looks like the end of the list. The extra 1rem
          // is the padding below, so the visible card matches those widths.
          //
          // p-2 is the room the hover tint sits in — the same 8px the grid gets
          // from its negative margin, taken from inside the slot instead. With
          // gap-2 between slots, two cards still sit 16px apart, as in the grid.
          className="w-[calc(70%+1rem)] shrink-0 snap-start p-2 min-[560px]:w-[calc(45%+1rem)] min-[1000px]:w-[calc(30%+1rem)] min-[1400px]:w-[calc(23%+1rem)]"
        >
          <VideoCard video={video} flush />
        </div>
      ))}
    </div>
  )
}
