import { useNavigate, useParams } from 'react-router-dom'
import { usePopular, useUpNext, useVideo } from '@/features/catalog/application/queries'
import { recordAutoplayHop } from '@/features/watch/application/autoplay'
import { useQueue } from '@/features/watch/application/queue'
import { arrivedByAdvancing, markAdvancedTo, useWatchTrail } from '@/features/watch/application/trail'
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
  // The last resort behind "next". Cheap and cached, and it is what keeps the
  // button alive once every suggestion has already been played this sitting.
  const { data: popular } = usePopular(50)
  const queue = useQueue(videoId)
  const trail = useWatchTrail(videoId)
  const navigate = useNavigate()

  // "Next" must always have an answer.
  //
  // Each step is a weaker claim than the one above it, and the list only ever
  // runs out if the library itself holds nothing else. A dead Next button is
  // worse than a repeat: someone playing music does not want the room to go
  // quiet because the recommender ran out of ideas it had not already used.
  //
  //   1. the queue, when playing through an explicit list
  //   2. up-next, minus anything already played this sitting — recommendations
  //      point both ways, so the untrimmed top of the rail is usually the video
  //      that was just playing, and following it walks in a two-video circle
  //   3. up-next unfiltered, once every suggestion has been seen
  //   4. popular, unseen first
  //   5. popular, anything at all
  const suggested = upNext?.filter((video) => !trail.has(video.id))
  const next =
    queue.next ??
    suggested?.[0] ??
    upNext?.find((video) => video.id !== videoId) ??
    popular?.find((video) => !trail.has(video.id) && video.id !== videoId) ??
    popular?.find((video) => video.id !== videoId)
  const nextInQueue = Boolean(queue.next)

  // Whether this video was arrived at by advancing rather than by being chosen.
  //
  // Advancing means "play me the next thing", so it starts at the beginning
  // even for a video watched before — dropping someone into the middle of a
  // track they did not pick reads as a glitch. Worse, a video watched to the
  // end has its position saved near the end, so resuming would run out almost
  // immediately and advance again, skating through the list.
  //
  // Read from sessionStorage rather than held in state here. Two previous
  // attempts kept this in React state and both lost it somewhere between the
  // navigation, the query refetch and the player mounting, in a way that could
  // not be reproduced by reading the code. A timestamped marker cannot be lost
  // by any of that, and expires on its own.
  const startAtBeginning = arrivedByAdvancing(videoId)

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
                  markAdvancedTo(next.id)
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
