import { Maximize, Pause, Play, Volume2, VolumeX } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import type { MediaState } from '@/features/catalog/domain/video'
import { useStream } from '@/features/catalog/application/queries'
import { httpCatalogRepository as repo } from '@/features/catalog/infrastructure/catalogRepository'
import { formatDuration } from '@/shared/lib/format'

/**
 * Phase 1 player: a progressive MP4 in a plain <video> element, served over
 * HTTP range requests. No HLS and no quality menu yet — Phase 1 stores a single
 * 1080p file, and a resolution picker with one entry would be a dead control.
 *
 * Controls are custom rather than native so the chrome matches the reference
 * design and so every action stays keyboard reachable for the TV interface.
 */
const PROGRESS_INTERVAL_MS = 15_000

export function Player({
  videoId,
  hue,
  durationSeconds,
  initialPositionSeconds,
  mediaState,
}: {
  videoId: string
  hue: number
  durationSeconds: number
  initialPositionSeconds: number
  mediaState: MediaState
}) {
  const { data: stream, isPending: resolvingStream, isError: streamFailed } = useStream(videoId)
  const videoRef = useRef<HTMLVideoElement>(null)
  const [playing, setPlaying] = useState(false)
  const [muted, setMuted] = useState(false)
  const [position, setPosition] = useState(initialPositionSeconds)
  // The catalog row can say READY while the file is missing from disk, for
  // example after a manual cleanup. Trust the element, not the metadata.
  const [loadFailed, setLoadFailed] = useState(false)

  // The real duration comes from the file once metadata loads. The catalog
  // value is only a placeholder for the first paint: reading it off the ref
  // would not re-render, which left the seek bar scaled to the wrong length.
  const [duration, setDuration] = useState(durationSeconds)

  const playable = Boolean(stream?.url) && !loadFailed

  // Resuming happens in onLoadedMetadata rather than in an effect: setting
  // currentTime before the browser knows the duration is silently ignored.

  // Report progress on a timer and on unmount, rather than on every timeupdate
  // event, so a watch session costs a handful of requests instead of hundreds.
  useEffect(() => {
    if (!playable) return

    const report = () => {
      const element = videoRef.current
      if (!element || !element.duration) return
      void repo
        .recordProgress(videoId, Math.floor(element.currentTime), element.currentTime / element.duration)
        .catch(() => {
          // Losing a progress ping degrades ranking slightly; never surface it.
        })
    }

    const timer = window.setInterval(report, PROGRESS_INTERVAL_MS)
    return () => {
      window.clearInterval(timer)
      report()
    }
  }, [videoId, playable])

  const toggle = () => {
    const element = videoRef.current
    if (!element) return
    if (element.paused) void element.play()
    else element.pause()
  }

  const seek = (event: React.ChangeEvent<HTMLInputElement>) => {
    const element = videoRef.current
    const next = Number(event.target.value)
    setPosition(next)
    if (element) element.currentTime = next
  }

  return (
    <div
      className="relative aspect-video w-full overflow-hidden rounded-xl bg-black"
      style={
        playable
          ? undefined
          : { background: `radial-gradient(120% 90% at 50% 30%, hsl(${hue} 40% 22%), #000 70%)` }
      }
    >
      {stream?.source === 'upstream' && (
        // Instant playback comes from upstream, which only publishes muxed
        // renditions at modest quality. Say so, rather than letting the user
        // wonder why it looks soft.
        <span className="absolute top-3 left-3 z-10 rounded bg-badge px-2 py-1 text-xs font-medium">
          Streaming {stream.height ? `${stream.height}p` : ''} while downloading
        </span>
      )}

      {playable ? (
        <video
          ref={videoRef}
          src={stream?.url}
          className="h-full w-full"
          playsInline
          preload="metadata"
          onPlay={() => setPlaying(true)}
          onPause={() => setPlaying(false)}
          onLoadedMetadata={(e) => {
            const element = e.currentTarget
            if (Number.isFinite(element.duration)) setDuration(element.duration)
            if (initialPositionSeconds > 0 && initialPositionSeconds < element.duration) {
              element.currentTime = initialPositionSeconds
            }
          }}
          onTimeUpdate={(e) => setPosition(e.currentTarget.currentTime)}
          onError={() => setLoadFailed(true)}
        />
      ) : (
        <p className="absolute inset-0 grid place-items-center px-6 text-center text-sm text-text-2">
          {resolvingStream
            ? 'Finding a stream…'
            : loadFailed
              ? 'The stream could not be loaded.'
              : mediaState === 'EVICTED'
                ? 'The media file was removed to reclaim disk space, and upstream has nothing directly playable. Re-download it to watch again.'
                : streamFailed
                  ? 'Nothing playable is available yet. The download has to finish first.'
                  : 'No media file available yet.'}
        </p>
      )}

      <div className="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/80 to-transparent px-3 pt-8 pb-2">
        <input
          type="range"
          min={0}
          max={Math.max(duration, 1)}
          value={position}
          onChange={seek}
          disabled={!playable}
          aria-label="Seek"
          className="h-1 w-full cursor-pointer accent-[var(--color-brand)]"
        />

        <div className="flex items-center gap-3 py-1.5 text-white">
          <button
            type="button"
            aria-label={playing ? 'Pause' : 'Play'}
            onClick={toggle}
            disabled={!playable}
            className="grid h-9 w-9 place-items-center rounded-full hover:bg-white/10"
          >
            {playing ? <Pause size={22} /> : <Play size={22} />}
          </button>
          <button
            type="button"
            aria-label={muted ? 'Unmute' : 'Mute'}
            onClick={() => {
              const element = videoRef.current
              if (element) {
                element.muted = !element.muted
                setMuted(element.muted)
              }
            }}
            disabled={!playable}
            className="grid h-9 w-9 place-items-center rounded-full hover:bg-white/10"
          >
            {muted ? <VolumeX size={22} /> : <Volume2 size={22} />}
          </button>
          <span className="text-xs tabular-nums">
            {formatDuration(position)} / {formatDuration(duration)}
          </span>
          <span className="flex-1" />
          <button
            type="button"
            aria-label="Full screen"
            onClick={() => void videoRef.current?.requestFullscreen?.()}
            disabled={!playable}
            className="grid h-9 w-9 place-items-center rounded-full hover:bg-white/10"
          >
            <Maximize size={22} />
          </button>
        </div>
      </div>
    </div>
  )
}
