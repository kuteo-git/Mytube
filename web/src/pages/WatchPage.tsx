import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useUpNext, useVideo } from '@/features/catalog/application/queries'
import { recordAutoplayHop } from '@/features/watch/application/autoplay'
import { useQueue } from '@/features/watch/application/queue'
import { useWatchTrail } from '@/features/watch/application/trail'
import { CommentSection } from '@/features/watch/ui/CommentSection'
import { DescriptionBox } from '@/features/watch/ui/DescriptionBox'
import { Player } from '@/features/watch/ui/Player'
import { QueueRail } from '@/features/watch/ui/QueueRail'
import { UpNextRail } from '@/features/watch/ui/UpNextRail'
import { VideoActions } from '@/features/watch/ui/VideoActions'
import { hueFromId } from '@/shared/lib/hue'

export function WatchPage() {
  const { videoId } = useParams()
  const { data: video, isPending, isError } = useVideo(videoId)
  const { data: upNext } = useUpNext(videoId)
  const queue = useQueue(videoId)
  const trail = useWatchTrail(videoId)
  const navigate = useNavigate()

  // A queue is an explicit instruction — "play this list, in this order" — so
  // it outranks the recommendation rail, which is only ever a suggestion.
  //
  // Without a queue, "next" is the top of the rail — but only counting videos
  // not already played this sitting. Recommendations point both ways, so the
  // untrimmed top of the rail is usually the video that was just playing, and
  // following it walks in a two-video circle.
  const suggested = upNext?.filter((video) => !trail.has(video.id))
  const next = queue.next ?? suggested?.[0]
  const nextInQueue = Boolean(queue.next)

  // The video we arrived at by advancing, rather than by choosing it.
  //
  // Advancing means "play me the next thing", so it starts at the beginning
  // even for a video watched before — dropping someone into the middle of a
  // track they did not pick reads as a glitch. Worse, a video watched to the
  // end has its position saved near the end, so resuming would run out almost
  // immediately and advance again, skating through the list.
  //
  // Deliberately not router state: that lives in history, so a reload or a
  // back-navigation would replay the fresh start and lose a real position.
  const [advancedTo, setAdvancedTo] = useState<string | null>(null)
  useEffect(() => {
    // Leaving that video for one nobody advanced to ends the exemption, so
    // returning to it later resumes normally.
    if (advancedTo && advancedTo !== videoId) setAdvancedTo(null)
  }, [videoId, advancedTo])
  const startAtBeginning = advancedTo === videoId

  if (isPending) {
    return (
      <div className="mx-auto max-w-[1754px] px-6 py-6">
        <div className="aspect-video w-full max-w-[1280px] animate-pulse rounded-xl bg-surface" />
      </div>
    )
  }

  if (isError || !video) {
    return <p className="p-16 text-center text-text-2">Video not found.</p>
  }

  return (
    <div className="mx-auto flex max-w-[1754px] flex-col gap-6 px-6 py-6 min-[1000px]:flex-row">
      <div className="min-w-0 max-w-[1280px] flex-1">
        <Player
          videoId={video.id}
          hue={hueFromId(video.id)}
          durationSeconds={video.durationSeconds}
          initialPositionSeconds={
            startAtBeginning ? 0 : (video.userState?.watchPositionSeconds ?? 0)
          }
          mediaState={video.mediaState}
          subtitles={video.subtitles}
          nextVideoTitle={next?.title}
          onPlayNext={
            next
              ? () => {
                  recordAutoplayHop()
                  setAdvancedTo(next.id)
                  // Staying inside the queue means carrying it along; a
                  // recommendation is a fresh start with no list.
                  navigate(`/watch/${next.id}${nextInQueue ? queue.search : ''}`)
                }
              : undefined
          }
        />

        <h1 className="mt-3 text-xl leading-7 font-bold">{video.title}</h1>

        <div className="mt-3">
          <VideoActions video={video} likeCount={video.likeCount} />
        </div>

        <div className="mt-3">
          <DescriptionBox video={video} />
        </div>

        <CommentSection videoId={video.id} />
      </div>

      <div className="w-full shrink-0 min-[1000px]:w-[402px]">
        {/* The queue replaces the recommendation rail rather than sitting
            beside it: while playing through a list, what comes next is already
            decided, and offering a competing list would just be noise. */}
        {queue.items.length > 0 ? (
          <QueueRail
            items={queue.items}
            currentIndex={queue.currentIndex}
            search={queue.search}
          />
        ) : (
          <UpNextRail current={video} exclude={trail} />
        )}
      </div>
    </div>
  )
}
