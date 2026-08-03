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
      // -m-4 p-4: sixteen of padding, cancelled by sixteen of margin.
      //
      // The cards bleed 8px past their slots on every side so their hover tint
      // has a body without costing layout, and a scroller clips that bleed —
      // overflow-x-auto clips vertically as well as scrolling horizontally.
      //
      // Eight of padding was not enough, which is why this took two goes: it
      // put the clip edge exactly on the card's own edge, so the tint met the
      // boundary with nothing to spare, and a rounded corner against a hard
      // edge reads as a crop. Sixteen leaves eight of clearance all round.
      //
      // The margin gives every pixel of it back, so the cards still line up
      // with the feed grid below, and on a phone the row still reaches the
      // screen edge — where a scrolling row that stops short reads as having
      // ended rather than as continuing (CLAUDE.md §8b).
      //
      // Four more on the left than the other three sides. That edge is the only
      // one a card is ever seen resting against — the row starts there and stays
      // there until someone scrolls — so it is the only one where eight of
      // clearance still reads as tight. The extra is cancelled the same way, so
      // nothing about the alignment moves.
      className="-m-4 -ml-5 flex snap-x snap-mandatory gap-4 overflow-x-auto p-4 pl-5 no-scrollbar"
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
