import clsx from 'clsx'
import { Hash, Search, Tv, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useSuggestions } from '@/features/catalog/application/queries'
import type { Suggestion } from '@/features/catalog/infrastructure/catalogRepository'

/**
 * Search with type-ahead over the local library.
 *
 * Suggestions deliberately do not come from YouTube. Proposing a term the
 * library has no video for would send every one of them to an empty page; a
 * suggestion that leads nowhere is worse than no suggestion.
 *
 * The listbox is keyboard-first — arrows, Enter, Escape — because that is what
 * a TV remote maps onto, not because a mouse user needs it.
 */
export function SearchBox() {
  const [query, setQuery] = useState('')
  const [open, setOpen] = useState(false)
  const [highlighted, setHighlighted] = useState(-1)

  const navigate = useNavigate()
  const containerRef = useRef<HTMLDivElement>(null)
  const { data: suggestions } = useSuggestions(query)

  const items = open ? (suggestions ?? []) : []

  useEffect(() => {
    setHighlighted(-1)
  }, [query])

  useEffect(() => {
    const onPointerDown = (event: MouseEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onPointerDown)
    return () => document.removeEventListener('mousedown', onPointerDown)
  }, [])

  const go = (term: string, suggestion?: Suggestion) => {
    setOpen(false)
    setQuery(term)
    // A topic is a place, not a phrase: jumping straight to it is what the
    // person meant, and it always has videos behind it.
    if (suggestion?.kind === 'TOPIC') {
      navigate(`/topic/${encodeURIComponent(term)}`)
      return
    }
    navigate(`/results?q=${encodeURIComponent(term)}`)
  }

  const onKeyDown = (event: React.KeyboardEvent) => {
    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault()
        setHighlighted((i) => (items.length === 0 ? -1 : (i + 1) % items.length))
        break
      case 'ArrowUp':
        event.preventDefault()
        setHighlighted((i) => (items.length === 0 ? -1 : (i - 1 + items.length) % items.length))
        break
      case 'Escape':
        setOpen(false)
        break
      case 'Enter':
        event.preventDefault()
        if (highlighted >= 0 && items[highlighted]) go(items[highlighted].text, items[highlighted])
        else if (query.trim()) go(query.trim())
        break
    }
  }

  return (
    <div ref={containerRef} className="mx-auto flex max-w-[720px] flex-1 items-center gap-2 min-w-0">
      <div className="relative flex-1 min-w-0">
        <div className="flex h-10 items-center rounded-full border border-line bg-surface-input focus-within:border-ring">
          <input
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setOpen(true)
            }}
            onFocus={() => setOpen(true)}
            onKeyDown={onKeyDown}
            placeholder="Search"
            aria-label="Search the library"
            aria-expanded={items.length > 0}
            aria-controls="search-suggestions"
            aria-autocomplete="list"
            role="combobox"
            className="h-full min-w-0 flex-1 rounded-l-full bg-transparent px-5 text-base outline-none placeholder:text-text-2"
          />

          {query && (
            <button
              type="button"
              aria-label="Clear search"
              onClick={() => {
                setQuery('')
                setOpen(false)
              }}
              className="grid h-8 w-8 place-items-center rounded-full text-text-2 transition-colors duration-150 ease-out hover:bg-surface-hover hover:text-text"
            >
              <X size={18} />
            </button>
          )}

          <button
            type="button"
            aria-label="Search"
            onClick={() => query.trim() && go(query.trim())}
            className="grid h-full w-16 place-items-center rounded-r-full border-l border-line bg-surface transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            <Search size={20} />
          </button>
        </div>

        {items.length > 0 && (
          <ul
            id="search-suggestions"
            role="listbox"
            className="absolute inset-x-0 top-12 z-40 overflow-hidden rounded-xl bg-surface py-2 shadow-2xl"
          >
            {items.map((suggestion, index) => (
              <li key={`${suggestion.kind}-${suggestion.text}`} role="option" aria-selected={index === highlighted}>
                <button
                  type="button"
                  onMouseEnter={() => setHighlighted(index)}
                  onClick={() => go(suggestion.text, suggestion)}
                  className={clsx(
                    'flex w-full items-center gap-4 px-4 py-1.5 text-left transition-colors duration-150 ease-out',
                    index === highlighted && 'bg-surface-hover',
                  )}
                >
                  <SuggestionIcon kind={suggestion.kind} />
                  <span className="clamp-1 flex-1 text-base">
                    <Highlighted text={suggestion.text} match={query} />
                  </span>
                  {suggestion.kind !== 'TITLE' && (
                    <span className="shrink-0 text-xs text-text-2">
                      {suggestion.videoCount} videos
                    </span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {/* No voice search button. It had no handler at all — a control that did
          nothing on either platform, which CLAUDE.md §5 forbids, and one more
          thing pushing the row past the width of a phone. */}
    </div>
  )
}

function SuggestionIcon({ kind }: { kind: Suggestion['kind'] }) {
  const className = 'shrink-0 text-text-2'
  if (kind === 'TOPIC') return <Hash size={18} className={className} />
  if (kind === 'CHANNEL') return <Tv size={18} className={className} />
  return <Search size={18} className={className} />
}

/** Bolds the part that is not yet typed, matching the reference screenshot. */
function Highlighted({ text, match }: { text: string; match: string }) {
  const index = text.toLowerCase().indexOf(match.toLowerCase())
  if (index < 0 || !match) return <>{text}</>

  return (
    <>
      {text.slice(0, index)}
      <span className="font-medium">{text.slice(index, index + match.length)}</span>
      {text.slice(index + match.length)}
    </>
  )
}
