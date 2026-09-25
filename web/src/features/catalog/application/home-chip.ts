import { ALL_CATEGORY } from '@/features/catalog/ui/ChipBar'

/**
 * Where Home's chip selection lives.
 *
 * In the address, because that is the only place a selection survives a reload
 * — it used to be React state, and picking Missed and pressing refresh put the
 * row back to All with nothing to say why.
 *
 * A search parameter rather than a path: `/topic/:name` already exists and is
 * linked from the sidebar and the search box, but it says "this is a topic",
 * which Missed and Live are not. One parameter answers for all of them.
 */
const CHIP_PARAM = 'chip'

/**
 * The chip a Home address names.
 *
 * `topicName` is the `/topic/:name` route's own parameter and wins outright:
 * that route names the selection by itself, and a `?chip=` left over from
 * somewhere else must not argue with it.
 */
export function chipFromSearch(search: URLSearchParams, topicName: string | undefined): string {
  if (topicName) return topicName
  return search.get(CHIP_PARAM) || ALL_CATEGORY
}

/**
 * The address for a chip, keeping whatever else was already in it.
 *
 * All is written as *absent* rather than as `chip=All`: a default spelled out
 * gives two addresses for one page, and the one a fresh visit produces is the
 * short one.
 *
 * The argument is copied rather than edited, because `useSearchParams` hands
 * back the live object and mutating it changes what the page believes before
 * anything has navigated.
 */
export function searchForChip(search: URLSearchParams, chip: string): URLSearchParams {
  const next = new URLSearchParams(search)
  if (chip === ALL_CATEGORY) next.delete(CHIP_PARAM)
  else next.set(CHIP_PARAM, chip)
  return next
}
