import type { Video } from '@/features/catalog/domain/video'
import { VideoCard, type VideoCardVariant } from '@/features/catalog/ui/VideoCard'

/**
 * A row of videos that scrolls sideways instead of pushing the page down.
 *
 * A section above the feed competes with it for the first screen. As a grid,
 * twelve cards is four rows on a desktop and twelve on a phone, so whatever it
 * offered arrived instead of the feed rather than before it. A rail costs one
 * row however many it holds.
 */
export function VideoRail({ videos, variant }: { videos: Video[]; variant?: VideoCardVariant }) {
  if (videos.length === 0) return null

  return (
    <div
      // The padding is the page's own gutter, and the margin gives all of it
      // back. Two things fall out of that, and both were wrong before.
      //
      // The cards line up with the feed grid below: content starts exactly
      // where the page's content starts, which is where the grid's first card
      // starts, so the two sections share one left edge.
      //
      // And the cards have somewhere to overhang into. They hang 8px outside
      // their slots so the hover tint has a body without costing layout, and a
      // scroller clips whatever leaves it — a gutter's worth of padding is
      // comfortably more than 8px, so the first card is no longer cut down its
      // left side the moment it lights up. Earlier attempts sized this padding
      // to the overhang itself, which put the clip edge exactly on the card's
      // edge: nothing removed, but a rounded corner flush against a hard
      // boundary reads as a crop.
      //
      // Vertically the same trick at 16px, which is the room the overhang needs
      // top and bottom without the row growing.
      //
      // scroll-pl is what makes any of that visible, and its absence is why
      // four goes at bigger padding changed nothing. Snapping aligns an item's
      // start with the scrollport's start, so on load the browser scrolled the
      // padding out of view — measured: scrollLeft sat at exactly the padding,
      // 24px, which is exactly how far the first card was displaced. Padding
      // added to the box was padding the snap immediately scrolled away.
      // scroll-padding moves the line the snap aligns to, so the room stays on
      // screen.
      // Proximity, not mandatory.
      //
      // Mandatory means the browser must always come to rest on a card, so it
      // takes hold of the row from the first pixel of sideways movement and
      // fights anything else the finger is doing. Proximity snaps only when the
      // scroll has already stopped near a card, which is the part worth having;
      // it also lets a fast flick travel across several cards instead of being
      // caught by the next one.
      className="-mx-4 -my-4 flex snap-x snap-proximity gap-4 overflow-x-auto overscroll-x-contain px-4 py-4
                 scroll-pl-4 no-scrollbar min-[700px]:-mx-6 min-[700px]:px-6
                 min-[700px]:scroll-pl-6"
    >
      {videos.map((video) => (
        <div
          key={video.id}
          // A width, because a flex child of a scroller shrinks to its content
          // otherwise. Sized so the next card is always part-visible: a row cut
          // exactly at the edge looks like the end of the list.
          className="w-[70%] shrink-0 snap-start min-[560px]:w-[45%] min-[1000px]:w-[30%] min-[1400px]:w-[23%]"
        >
          <VideoCard video={video} variant={variant} />
        </div>
      ))}
    </div>
  )
}
