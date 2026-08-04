import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useRememberedScrollX } from '@/features/navigation/application/use-remembered-scroll-x'
import { Pill } from '@/shared/ui/primitives'

/** Horizontally scrollable category filter, scrollbar hidden, arrows on both ends. */
export function ChipBar({
  categories,
  active,
  onSelect,
}: {
  categories: string[]
  active: string
  onSelect: (category: string) => void
}) {
  // Where this row was left, kept across the page being unmounted. Switching
  // tabs rebuilds Home, and without this the chips came back at the beginning —
  // losing exactly the one you had scrolled across to find.
  const { ref: scroller, attach } = useRememberedScrollX('yt-chipbar-x')

  const scrollBy = (delta: number) =>
    scroller.current?.scrollBy({ left: delta, behavior: 'smooth' })

  return (
    // Edge to edge on a phone.
    //
    // The page's own padding would otherwise stop the row short of both sides,
    // and a horizontal scroller that stops short reads as a list that has ended
    // rather than one that continues. The negative margin takes it back out to
    // the edges and the padding inside puts the first chip where the text above
    // it starts, so nothing is flush against the glass.
    // `top-0`, and it is the top bar's height that must NOT appear here.
    //
    // The sticky threshold is measured from the scroller's content edge, and
    // the scroller already carries `pt-14` to clear the bar — so zero here
    // already means "just below the bar". Writing the bar's height as well made
    // the threshold 112px, and sticky does not only hold an element up, it
    // pushes one down to reach its threshold: the row sat a whole header lower
    // than it should, visibly so before any scrolling had happened.
    //
    // That is the third form of one mistake in this project's history —
    // counting the top bar twice. WatchPage's reserve did it, this row did it
    // when the bar was sticky, and it did it again when the bar became an
    // overlay. The number belongs in exactly one place, and that place is the
    // scroller.
    //
    // Opaque, and deliberately not blurred like the bar above it.
    //
    // Two adjacent `backdrop-filter` layers can never line up: each blurs its
    // own backdrop and then clips to its own bounds, so along the shared edge
    // the two are inventing pixels from different neighbourhoods. The seam is
    // in the technique, not in the parameters — matching the radius and the
    // tint does not remove it. One opaque surface next to one blurred one at
    // least reads as a decision.
    //
    // No horizontal padding here: it belongs to the scrolling row, below.
    <div
      className="sticky top-0 z-10 -mx-4 flex items-center gap-2 bg-bg py-3
                 min-[700px]:mx-0"
    >
      <button
        type="button"
        aria-label="Scroll categories left"
        onClick={() => scrollBy(-320)}
        // The arrows are for a mouse; a finger just drags the row.
        className="hidden h-8 w-8 shrink-0 place-items-center rounded-full hover:bg-surface-hover min-[700px]:grid"
      >
        <ChevronLeft size={20} />
      </button>

      <div
        ref={attach}
        role="tablist"
        aria-label="Categories"
        // The inset lives on the scroller, not on the wrapper around it.
        //
        // On the wrapper it shortened the scrolling region itself, so a chip
        // vanished 16px before the edge of the screen and the row read as
        // clipped rather than as continuing. Here the region runs edge to edge
        // — which is what the note above always claimed — and the padding is
        // simply where the first and last chip come to rest.
        //
        // `scroll-px-4` is the companion: it tells the browser the same 16px is
        // reserved when *it* scrolls the row, so the arrow buttons and keyboard
        // focus land a chip clear of the edge instead of flush against it.
        className="flex flex-1 gap-3 overflow-x-auto overscroll-x-contain no-scrollbar
                   px-4 scroll-px-4 min-[700px]:px-0 min-[700px]:scroll-px-0"
      >
        {categories.map((category) => (
          <Pill
            key={category}
            role="tab"
            aria-selected={category === active}
            active={category === active}
            onClick={() => onSelect(category)}
          >
            {category}
          </Pill>
        ))}
      </div>

      <button
        type="button"
        aria-label="Scroll categories right"
        onClick={() => scrollBy(320)}
        className="hidden h-8 w-8 shrink-0 place-items-center rounded-full hover:bg-surface-hover min-[700px]:grid"
      >
        <ChevronRight size={20} />
      </button>
    </div>
  )
}
