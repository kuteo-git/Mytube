import { ChevronLeft, ChevronRight } from 'lucide-react'
import { useRef } from 'react'
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
  const scroller = useRef<HTMLDivElement>(null)

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
    <div
      className="sticky top-14 z-10 -mx-4 flex items-center gap-2 bg-bg px-4 py-3
                 min-[700px]:mx-0 min-[700px]:px-0"
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
        ref={scroller}
        role="tablist"
        aria-label="Categories"
        className="flex flex-1 gap-3 overflow-x-auto overscroll-x-contain no-scrollbar"
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
