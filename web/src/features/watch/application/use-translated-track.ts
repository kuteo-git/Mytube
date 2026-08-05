import { useEffect, useRef, useState } from 'react'
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
): number {
  const client = useQueryClient()
  const announced = useRef({ first: false, complete: false })
  const [revision, setRevision] = useState(0)

  useEffect(() => {
    announced.current = { first: false, complete: false }
    setRevision(0)
  }, [videoId])

  useEffect(() => {
    const seen = announced.current
    const first = vttVersion > 0 && !seen.first
    const last = complete && vttVersion > 0 && !seen.complete
    if (!first && !last) return

    if (first) seen.first = true
    if (last) seen.complete = true
    void client.invalidateQueries({ queryKey: ['video', videoId] })
    // The list coming back is not enough on its own.
    //
    // The file keeps the same name while it is rewritten after every batch, and
    // a browser will not fetch a `<track>` address it already has — so the copy
    // on screen stayed the handful of lines that existed when it was first
    // asked for. Watching a translation finish and then finding the subtitles
    // stop part way through was that, and reloading the page "fixed" it because
    // a reload is the one thing that fetches the file again.
    //
    // This number goes on the address, which is what makes it a different one.
    setRevision((n) => n + 1)
  }, [client, videoId, vttVersion, complete])

  return revision
}

/**
 * The address to give a `<track>`, with the revision on it where one is needed.
 *
 * Only the generated Vietnamese track is rewritten in place; every other
 * subtitle is written once when the video is downloaded and never changes, so
 * putting a version on those would only defeat the browser's cache for nothing.
 */
export function trackURL(
  url: string,
  generated: boolean,
  revision: number,
): string {
  if (!generated || revision === 0) return url
  return `${url}${url.includes('?') ? '&' : '?'}v=${revision}`
}
