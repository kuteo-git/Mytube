import { useNavigate, useParams } from 'react-router-dom'
import { useUpNext, useVideo } from '@/features/catalog/application/queries'
import { recordAutoplayHop } from '@/features/watch/application/autoplay'
import { CommentSection } from '@/features/watch/ui/CommentSection'
import { DescriptionBox } from '@/features/watch/ui/DescriptionBox'
import { Player } from '@/features/watch/ui/Player'
import { UpNextRail } from '@/features/watch/ui/UpNextRail'
import { VideoActions } from '@/features/watch/ui/VideoActions'
import { hueFromId } from '@/shared/lib/hue'

export function WatchPage() {
  const { videoId } = useParams()
  const { data: video, isPending, isError } = useVideo(videoId)
  const { data: upNext } = useUpNext(videoId)
  const navigate = useNavigate()
  const next = upNext?.[0]

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
          initialPositionSeconds={video.userState?.watchPositionSeconds ?? 0}
          mediaState={video.mediaState}
          subtitles={video.subtitles}
          nextVideoTitle={next?.title}
          onPlayNext={
            next
              ? () => {
                  recordAutoplayHop()
                  navigate(`/watch/${next.id}`)
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
        <UpNextRail current={video} />
      </div>
    </div>
  )
}
