import clsx from 'clsx'
import { useEffect } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { usePopular, useUpNext, useVideo } from '@/features/catalog/application/queries'
import { recordAutoplayHop } from '@/features/watch/application/autoplay'
import { useQueue } from '@/features/watch/application/queue'
import { arrivedByAdvancing, markAdvancedTo, useWatchTrail } from '@/features/watch/application/trail'
import { usePlayer } from '@/features/watch/application/player-context'
import { CommentSection } from '@/features/watch/ui/CommentSection'
import { DescriptionBox } from '@/features/watch/ui/DescriptionBox'
import { QueueRail } from '@/features/watch/ui/QueueRail'
import { UpNextRail } from '@/features/watch/ui/UpNextRail'
import { VideoActions } from '@/features/watch/ui/VideoActions'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'

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
  const { slotRef, activate } = usePlayer()

  // "Next" must always have an answer.
  //
  // Each step is a weaker claim than the one above it, and the list only ever
  // runs out if the library itself holds nothing else. A dead Next button is
  // worse than a repeat: someone playing music does not want the room to go
  // quiet because the recommender ran out of ideas it had not already used.
  //
  //   1. the queue, when playing through an explicit list
  //   2. the best suggestion not already played this sitting
  //   3. anything popular not already played this sitting
  //   4. only now, something already seen
  //
  // Every "not already played" option comes before every "already played" one,
  // and that ordering is the point rather than a preference. An earlier version
  // put unfiltered suggestions ahead of unseen popular ones, so the moment the
  // suggestions ran dry it started handing back the video just watched — which
  // is a loop, reintroduced by the very fallback meant to stop the button dying.
  // Suggestions are better than popularity, but nothing is worse than going
  // round in a circle.
  const unseen = (video: { id: string }) => !trail.has(video.id) && video.id !== videoId
  const next =
    queue.next ??
    upNext?.find(unseen) ??
    popular?.find(unseen) ??
    upNext?.find((video) => video.id !== videoId) ??
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

  const onPlayNext = next
    ? () => {
        recordAutoplayHop()
        markAdvancedTo(next.id)
        // Staying inside the queue means carrying it along; a
        // recommendation is a fresh start with no list.
        navigate(`/watch/${next.id}${nextInQueue ? queue.search : ''}`)
      }
    : undefined

  // Register the video with the player context so AppShell can render it.
  // The Player instance lives in AppShell and is portal'd here.
  useEffect(() => {
    if (!video || isPending || isError) return
    activate({
      videoId: video.id,
      title: video.title,
      channelTitle: video.channel.name,
      hue: hueFromId(video.id),
      durationSeconds: video.durationSeconds,
      initialPositionSeconds: startAtBeginning ? 0 : (video.userState?.watchPositionSeconds ?? 0),
      mediaState: video.mediaState,
      subtitles: video.subtitles,
      thumbnailURL: mediaURL(video.thumbnailPath) || undefined,
      nextVideoTitle: next?.title,
      onPlayNext,
    })
  }, [video, isPending, isError, startAtBeginning, next?.title, activate])

  // Leaving the watch page needs no teardown. The player host lives in AppShell
  // and works out its own shape from the route, so unmounting this page simply
  // takes the slot away and the host moves to the corner. An earlier version
  // tore the player down here and rebuilt it elsewhere, which is precisely what
  // stopped the video.

  if (isPending) {
    return (
      <div className="mx-auto max-w-[1754px] px-4 py-6 min-[700px]:px-6">
        <div className="aspect-video w-full max-w-[1280px] animate-pulse rounded-xl bg-surface" />
      </div>
    )
  }

  if (isError || !video) {
    return <p className="p-16 text-center text-text-2">Video not found.</p>
  }

  return (
    <div
      className="mx-auto flex max-w-[1754px] flex-col gap-6 px-4 py-0 min-[700px]:px-6 min-[700px]:py-6
                 min-[1000px]:flex-row
                 pt-[56.25vw] min-[700px]:pt-6"
    >
      {/* The reserve is the player's height and nothing else.

          It used to add the top bar's height as well, which was counted twice:
          the bar is `sticky`, and a sticky element still takes its place in the
          flow, so the content already begins below it. The result was a gap of
          exactly one top bar between the picture and the title.

          56.25vw is 9/16 of the width — the same 16:9 the player host computes
          for itself on a phone. Two expressions of one number, which is a thing
          to be careful of: changing the shape means changing both. */}
      <div className="min-w-0 max-w-[1280px] flex-1">
        {/* The player's slot. AppShell measures this div and positions the player
            host over it — the player is never a child of this page, which is what
            lets it outlive the page. On mobile the host is pinned below the top
            bar instead, so the slot only reserves the space it would occupy. */}
        <div
          ref={slotRef}
          data-testid="player-slot"
          className={clsx('hidden aspect-video w-full rounded-xl bg-black min-[700px]:block')}
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
