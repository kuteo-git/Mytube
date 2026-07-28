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
    <div className="sticky top-14 z-10 flex items-center gap-2 bg-bg py-3">
      <button
        type="button"
        aria-label="Scroll categories left"
        onClick={() => scrollBy(-320)}
        className="grid h-8 w-8 shrink-0 place-items-center rounded-full hover:bg-surface-hover"
      >
        <ChevronLeft size={20} />
      </button>

      <div
        ref={scroller}
        role="tablist"
        aria-label="Categories"
        className="flex flex-1 gap-3 overflow-x-auto no-scrollbar"
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
        className="grid h-8 w-8 shrink-0 place-items-center rounded-full hover:bg-surface-hover"
      >
        <ChevronRight size={20} />
      </button>
    </div>
  )
}
