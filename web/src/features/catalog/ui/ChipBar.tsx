import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useRememberedScrollX } from '@/features/navigation/application/use-remembered-scroll-x'
import { Pill } from '@/shared/ui/primitives'

/**
 * The chip that means "on air".
 *
 * Named here rather than passed in, because the alternative — a prop carrying
 * arbitrary content per chip — would let any caller put anything beside any
 * label, and there is exactly one chip in this app that is not a topic name.
 */
export const LIVE_CATEGORY = 'Live'

/**
 * A lit dot with a ring leaving it.
 *
 * The dot itself never moves, dims, or blinks: this row is scanned at speed,
 * and a dot that spends half its time faded is one you can miss entirely. The
 * ring is a separate absolutely-positioned element so the dot's own box never
 * changes size and the label beside it cannot be nudged.
 *
 * `motion-safe:` and not the global reduced-motion rule — that one shortens
 * animations rather than removing them, which turns anything infinite into a
 * strobe.
 */
function LiveDot() {
  return (
    <span className="relative grid h-2 w-2 shrink-0 place-items-center" aria-hidden>
      <span className="absolute h-2 w-2 rounded-full bg-brand motion-safe:animate-[live-ring_1.8s_ease-out_infinite]" />
      <span className="relative h-2 w-2 rounded-full bg-brand" />
    </span>
  )
}

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
    // the scroller already carries `--top-bar` to clear the bar — so zero here
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
    // The same translucent surface as the bar above it, from the same class.
    //
    // Opaque at first, because two adjacent `backdrop-filter` layers can never
    // line up: each blurs its own backdrop and clips to its own bounds, so
    // along the shared edge the two invent pixels from different
    // neighbourhoods. That objection was made at 70%, and at 95% it is worth
    // very little — a twentieth of what is behind comes through, so there is a
    // twentieth as much mismatch to notice.
    //
    // The chips themselves stay opaque pills, so their text never sits on
    // anything but `--surface`. Only the strip behind them is see-through.
    //
    // No horizontal padding here: it belongs to the scrolling row, below.
    <div
      className="chrome-blur sticky top-0 z-10 -mx-4 flex items-center gap-2 py-3
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
            {category === LIVE_CATEGORY && <LiveDot />}
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
