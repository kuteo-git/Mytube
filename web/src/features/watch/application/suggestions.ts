import { useCallback, useEffect, useState, useSyncExternalStore } from 'react'
import { useIsFullscreen } from '@/features/watch/application/player-presentation'

const STORAGE_KEY = 'suggestions-hidden'

/**
 * Whether the suggestions column beside the video is showing.
 *
 * A module-level store rather than `useState` per caller, which is what
 * `useAutoplayPreference` does — and the difference is not style. The switch
 * lives on the player, which AppShell renders, while the column it hides is
 * rendered by the watch page; two independent `useState`s would each hold their
 * own copy and the button would light up beside a rail that never moved.
 *
 * Remembered, because it is a statement about how somebody wants to watch and
 * not about one video. The `storage` event carries it to other tabs for free.
 */
let hidden = readStored()
const listeners = new Set<() => void>()

function readStored(): boolean {
  try {
    return window.localStorage.getItem(STORAGE_KEY) === 'on'
  } catch {
    // Private windows and blocked site data both throw here. A preference that
    // cannot be stored is not a reason to fail to draw the page.
    return false
  }
}

function emit() {
  for (const listener of listeners) listener()
}

if (typeof window !== 'undefined') {
  window.addEventListener('storage', (e) => {
    if (e.key !== STORAGE_KEY) return
    hidden = readStored()
    emit()
  })
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function setSuggestionsHidden(next: boolean) {
  if (next === hidden) return
  hidden = next
  try {
    window.localStorage.setItem(STORAGE_KEY, next ? 'on' : 'off')
  } catch {
    // Kept in memory for this sitting even when it cannot be written down.
  }
  emit()
}

export function useSuggestionsHidden(): [boolean, (next: boolean) => void] {
  const value = useSyncExternalStore(
    subscribe,
    () => hidden,
    () => false,
  )
  const set = useCallback((next: boolean) => setSuggestionsHidden(next), [])
  return [value, set]
}

/**
 * The width at which the watch page becomes two columns.
 *
 * It has to match `min-[1000px]:flex-row` on the layout in WatchPage, and that
 * class is compiled by Tailwind so it cannot read this number. The comment
 * there names this constant; changing one means changing both.
 */
export const TWO_COLUMN_WIDTH = 1000

/**
 * Whether a page wide enough and a screen not filled by the video mean there is
 * a column beside it at all.
 *
 * Pure, and named, because it is the decision — not the arithmetic around it.
 * Nothing in the type system catches a control drawn where the thing it
 * changes is not on screen: it compiles, it renders, and the only symptom is a
 * press that does nothing.
 */
export function columnBesideVideo({ wide, fullscreen }: { wide: boolean; fullscreen: boolean }): boolean {
  return wide && !fullscreen
}

/**
 * Whether there is a column beside the video to show or hide at all.
 *
 * Narrower than the breakpoint the rail is stacked under the video, so the
 * switch would be a control for a layout that is not on screen — and below
 * 700px the page is the phone shape entirely.
 *
 * Full screen is the same answer arriving from the other direction, and it was
 * missed the first time: the window is still 1440px wide there, so a rule that
 * asked only about width said yes while the whole screen was the picture. The
 * button was drawn over the video and pressing it changed nothing a viewer
 * could see — measured, the rail stayed 402px and the picture stayed 1440px
 * either way. That is the dead button §5 of the charter forbids outright.
 */
export function useSuggestionsToggleAvailable(): boolean {
  const fullscreen = useIsFullscreen()
  const [wide, setWide] = useState(() => {
    if (typeof matchMedia !== 'function') return true
    return matchMedia(`(min-width: ${TWO_COLUMN_WIDTH}px)`).matches
  })

  useEffect(() => {
    if (typeof matchMedia !== 'function') return
    const query = matchMedia(`(min-width: ${TWO_COLUMN_WIDTH}px)`)
    const read = () => setWide(query.matches)
    read()
    query.addEventListener('change', read)
    return () => query.removeEventListener('change', read)
  }, [])

  return columnBesideVideo({ wide, fullscreen })
}
