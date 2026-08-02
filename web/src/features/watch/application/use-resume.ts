import { useEffect, useState } from 'react'
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
export function useResumeLastWatched(isWatch: boolean): void {
  const { state, activate } = usePlayer()

  // Read once. Progress reporting rewrites this entry every fifteen seconds, so
  // reading it on every render would mean resuming to a moving target.
  const [entry] = useState(readLastWatched)

  const wanted = entry && !isWatch && !state ? entry.videoId : undefined
  const { data: video } = useVideo(wanted)

  useEffect(() => {
    if (!video || !entry || state) return

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
}
