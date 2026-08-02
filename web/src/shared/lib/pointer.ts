import { useEffect, useState } from 'react'

/**
 * Whether the primary pointer is a finger.
 *
 * Asked of the device rather than of the screen's width, because the question
 * is what is doing the pointing. A small window on a laptop still has a mouse,
 * and a tablet the size of a laptop still does not.
 *
 * This drives layout — sizes and which controls appear — so it has to be a
 * standing answer that survives re-renders. Deciding what a single tap meant is
 * a different question, answered per event from the pointer that caused it.
 */
export function useCoarsePointer(): boolean {
  const [coarse, setCoarse] = useState(() => matches())

  useEffect(() => {
    if (typeof matchMedia !== 'function') return
    const query = matchMedia('(pointer: coarse)')
    const onChange = () => setCoarse(query.matches)
    query.addEventListener?.('change', onChange)
    return () => query.removeEventListener?.('change', onChange)
  }, [])

  return coarse
}

function matches(): boolean {
  if (typeof matchMedia !== 'function') return false
  return matchMedia('(pointer: coarse)').matches
}
