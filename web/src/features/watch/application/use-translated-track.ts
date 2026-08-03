import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'

/**
 * Ask for the subtitle list again once a translation has been written.
 *
 * The list arrives with the video and is fetched once. The translated track
 * appears later — the first time somebody narrates the video — so a viewer who
 * sat and watched a translation finish still had no VI option to choose until
 * they reloaded the page. That was the bug.
 *
 * Keyed on the file having actually been written rather than on the count
 * moving: the write is a request of its own, and asking the gateway for the
 * track list before it lands would just be told there is no file yet.
 *
 * Twice at most. The first write is what makes the option appear; the last is
 * what makes it complete. Refetching after every batch would put a request on
 * the wire every fifteen seconds for a list that changed once.
 */
export function useTranslatedTrack(
  videoId: string,
  vttVersion: number,
  complete: boolean,
): void {
  const client = useQueryClient()
  const announced = useRef({ first: false, complete: false })

  useEffect(() => {
    announced.current = { first: false, complete: false }
  }, [videoId])

  useEffect(() => {
    const seen = announced.current
    const first = vttVersion > 0 && !seen.first
    const last = complete && vttVersion > 0 && !seen.complete
    if (!first && !last) return

    if (first) seen.first = true
    if (last) seen.complete = true
    void client.invalidateQueries({ queryKey: ['video', videoId] })
  }, [client, videoId, vttVersion, complete])
}
