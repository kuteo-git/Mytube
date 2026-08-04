import { useEffect, useRef, useState } from 'react'
import { useVideo } from '@/features/catalog/application/queries'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'
import { readLastWatched } from './last-watched'
import { usePlayer } from './player-context'

/**
 * Offers back whatever this browser was in the middle of.
 *
 * Opening the app again puts the video that was playing into the corner, paused,
 * with the position it had reached. Closing a tab is rarely a decision to stop
 * watching; it is usually a decision to do something else first, and starting
 * blank throws that away.
 *
 * Paused, and never started on its own. Sound arriving unbidden from a corner of
 * a page somebody has only just opened is startling, and it would be the second
 * time in this project that a video played when nobody asked it to.
 *
 * Not on a watch page: that page activates the player itself, with more than
 * this knows — what plays next, what the queue holds. Stepping in first would
 * mean two answers to the same question.
 */
export function useResumeLastWatched(isWatch: boolean): boolean {
  const { state, activate } = usePlayer()

  // Read once. Progress reporting rewrites this entry every fifteen seconds, so
  // reading it on every render would mean resuming to a moving target.
  const [entry] = useState(readLastWatched)

  // Offered once, and then never again for as long as this page is open.
  //
  // Without this the offer could not be refused. Closing the miniplayer clears
  // the player's state, which is exactly the condition this hook waits for, so
  // it put the video straight back — and the entry it was reading from is a
  // snapshot taken at mount, so clearing storage did not stop it either. The
  // close button worked perfectly and was undone within the same tick.
  const offered = useRef(false)

  // A player that arrived by any other route counts as the offer having been
  // taken up.
  //
  // The flag above only recorded this hook's own offer, and that was too narrow
  // by exactly one case: reload the watch page and the entry describing the
  // video on screen is already in storage, so the offer is armed while the watch
  // page — not this hook — activates the player. Leaving for the home page turns
  // that player into the corner window, and closing it produces precisely the
  // state this hook waits for. The video came straight back, so the close button
  // looked broken, and to a viewer who pressed it twice it looked like there had
  // been two players stacked in the corner all along.
  //
  // What the offer is for is a browser that is in the middle of something and
  // showing nothing. Once anything has been playing, it is not that.
  useEffect(() => {
    if (state) offered.current = true
  }, [state])

  const wanted = entry && !isWatch && !state && !offered.current ? entry.videoId : undefined
  const { data: video } = useVideo(wanted)

  useEffect(() => {
    if (!video || !entry || state || offered.current) return
    offered.current = true

    activate({
      videoId: video.id,
      title: video.title,
      channelTitle: video.channel.name,
      hue: hueFromId(video.id),
      durationSeconds: video.durationSeconds,
      // The server's position wins where it has one: it is the record of
      // watching, across every device, and this browser's copy exists only for
      // the case where the tab closed before the last report went out.
      initialPositionSeconds:
        video.userState?.watchPositionSeconds || entry.positionSeconds,
      mediaState: video.mediaState,
      subtitles: video.subtitles,
      thumbnailURL: mediaURL(video.thumbnailPath) || undefined,
      autoplay: false,
    })
  }, [video, entry, state, activate])

  // Whether a corner window is on its way.
  //
  // Known synchronously, at the first render, because the entry is read from
  // storage rather than fetched — and that matters: the page can leave room for
  // it from the start instead of reflowing when it arrives a moment later,
  // which is what made opening the app flash and settle.
  return Boolean(entry) && !isWatch && !offered.current
}
