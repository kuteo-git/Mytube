import {
  Captions,
  CaptionsOff,
  Maximize,
  Pause,
  Play,
  SkipForward,
  Volume1,
  Volume2,
  VolumeX,
} from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { MediaState, SubtitleTrack } from '@/features/catalog/domain/video'
import { useStream } from '@/features/catalog/application/queries'
import { useDownloadProgress } from '@/features/catalog/application/download'
import {
  autoplayChainExhausted,
  resetAutoplayChain,
  useAutoplayPreference,
} from '@/features/watch/application/autoplay'
import { httpCatalogRepository as repo } from '@/features/catalog/infrastructure/catalogRepository'
import { formatDuration } from '@/shared/lib/format'

/**
 * Progressive MP4 in a plain <video> element, served over HTTP range requests.
 * No HLS and no quality menu: one file exists per video, and a resolution
 * picker with a single entry would be a dead control.
 *
 * Controls are custom rather than native so the chrome matches the reference
 * design, and every action is reachable by keyboard — which is also what makes
 * the eventual TV interface possible.
 */
const PROGRESS_INTERVAL_MS = 15_000
const SEEK_STEP_SECONDS = 5

export function Player({
  videoId,
  hue,
  durationSeconds,
  initialPositionSeconds,
  mediaState,
  subtitles,
  nextVideoTitle,
  onPlayNext,
}: {
  videoId: string
  hue: number
  durationSeconds: number
  initialPositionSeconds: number
  mediaState: MediaState
  subtitles: SubtitleTrack[]
  nextVideoTitle?: string
  onPlayNext?: () => void
}) {
  const { data: stream, isPending: resolvingStream, isError: streamFailed } = useStream(videoId)
  // Playing from upstream always schedules a copy, so a job is coming even if
  // the queue has not caught up yet.
  const download = useDownloadProgress(videoId, stream?.source === 'upstream')
  const queryClient = useQueryClient()

  const videoRef = useRef<HTMLVideoElement>(null)
  const [playing, setPlaying] = useState(false)
  const [volume, setVolume] = useState(1)
  const [muted, setMuted] = useState(false)
  const [position, setPosition] = useState(initialPositionSeconds)
  const [buffered, setBuffered] = useState(0)
  const [duration, setDuration] = useState(durationSeconds)
  // The catalog row can say READY while the file is missing from disk, for
  // example after a manual cleanup. Trust the element, not the metadata.
  const [loadFailed, setLoadFailed] = useState(false)
  // Set when the browser refuses to start audible playback. Autoplay policies
  // require a gesture on the page the video is on, and arriving by navigation
  // does not count, so the fallback is to start muted and say so.
  const [autoplayMuted, setAutoplayMuted] = useState(false)
  // Language code of the active caption track, or null for off. Tracks arrive
  // shortly after playback starts, before the media file finishes downloading.
  const [captions, setCaptions] = useState<string | null>(null)
  const [autoplayEnabled, setAutoplayEnabled] = useAutoplayPreference()
  // Seconds left before the next video starts, or null when no countdown runs.
  const [countdown, setCountdown] = useState<number | null>(null)

  // Swapping the source reloads the element and resets currentTime. The
  // position is captured here just before the swap so playback can resume where
  // it was, rather than restarting the video the viewer was halfway through.
  const resumeAtRef = useRef(initialPositionSeconds)
  const wasPlayingRef = useRef(false)
  // One re-resolve is allowed per mounted player. An expired upstream URL is
  // the common failure and it fixes itself; anything that survives a fresh URL
  // is a real failure and must be shown rather than retried forever.
  const retriedRef = useRef(false)

  const playable = Boolean(stream?.url) && !loadFailed
  // Captions no longer wait for the media file: ingest publishes them ahead of
  // the transfer, precisely so they are usable during upstream playback.
  const captionsAvailable = subtitles.length > 0

  // <track> elements are declarative but their display is not: the browser
  // decides which one shows. Driving textTracks directly keeps the button and
  // what is on screen in agreement.
  useEffect(() => {
    const element = videoRef.current
    if (!element) return
    for (let i = 0; i < element.textTracks.length; i++) {
      const track = element.textTracks[i]
      track.mode = track.language === captions ? 'showing' : 'disabled'
    }
  }, [captions, stream?.url, subtitles.length])

  // Runs on the render *before* the new src is committed, so the element still
  // holds the old media and its clock is still meaningful.
  useEffect(() => {
    return () => {
      const element = videoRef.current
      if (!element || !Number.isFinite(element.currentTime)) return
      resumeAtRef.current = element.currentTime
      wasPlayingRef.current = !element.paused
    }
  }, [stream?.url])

  useEffect(() => {
    retriedRef.current = false
    setLoadFailed(false)
  }, [videoId])

  useEffect(() => {
    if (countdown === null) return
    if (countdown <= 0) {
      setCountdown(null)
      onPlayNext?.()
      return
    }
    const timer = window.setTimeout(() => setCountdown((n) => (n === null ? null : n - 1)), 1000)
    return () => window.clearTimeout(timer)
  }, [countdown, onPlayNext])

  const toggle = useCallback(() => {
    resetAutoplayChain()
    const element = videoRef.current
    if (!element) return
    if (element.paused) void element.play()
    else element.pause()
  }, [])

  const seekBy = useCallback((delta: number) => {
    resetAutoplayChain()
    const element = videoRef.current
    if (!element) return
    element.currentTime = Math.max(0, Math.min(element.duration || 0, element.currentTime + delta))
  }, [])

  const applyVolume = (next: number) => {
    resetAutoplayChain()
    const element = videoRef.current
    setVolume(next)
    if (element) {
      element.volume = next
      element.muted = next === 0
      setMuted(next === 0)
    }
    // Touching the volume is a gesture, so audible playback is allowed again.
    setAutoplayMuted(false)
  }

  const toggleMute = () => {
    const element = videoRef.current
    if (!element) return
    element.muted = !element.muted
    setMuted(element.muted)
    setAutoplayMuted(false)
  }

  // Keyboard control. Space and arrows are the conventions people already know,
  // and they are what a TV remote maps onto.
  useEffect(() => {
    if (!playable) return

    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA')) return

      switch (event.key) {
        case ' ':
        case 'k':
          event.preventDefault()
          toggle()
          break
        case 'ArrowLeft':
          event.preventDefault()
          seekBy(-SEEK_STEP_SECONDS)
          break
        case 'ArrowRight':
          event.preventDefault()
          seekBy(SEEK_STEP_SECONDS)
          break
        case 'm':
          toggleMute()
          break
      }
    }

    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [playable, toggle, seekBy])

  // Report progress on a timer and on unmount rather than on every timeupdate,
  // so a watch session costs a handful of requests instead of hundreds.
  useEffect(() => {
    if (!playable) return

    const report = () => {
      const element = videoRef.current
      if (!element || !element.duration) return
      void repo
        .recordProgress(
          videoId,
          Math.floor(element.currentTime),
          element.currentTime / element.duration,
        )
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

  const downloading = download?.state === 'RUNNING' || download?.state === 'QUEUED'
  const downloadPercent = Math.round((download?.progress ?? 0) * 100)

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
        <div className="absolute top-3 left-3 z-10 flex items-center gap-2 rounded-lg bg-badge px-2.5 py-1.5 text-xs font-medium">
          <span>Streaming{stream.height ? ` ${stream.height}p` : ''}</span>
          {downloading && (
            <>
              <span className="h-3 w-px bg-white/25" />
              <span className="flex items-center gap-1.5">
                <span
                  className="h-1 w-16 overflow-hidden rounded-full bg-white/25"
                  role="progressbar"
                  aria-label="Download progress"
                  aria-valuenow={downloadPercent}
                  aria-valuemin={0}
                  aria-valuemax={100}
                >
                  <span
                    className="block h-full rounded-full bg-brand transition-[width] duration-500 ease-out"
                    style={{ width: `${downloadPercent}%` }}
                  />
                </span>
                <span className="tabular-nums">{downloadPercent}%</span>
              </span>
            </>
          )}
        </div>
      )}

      {autoplayMuted && playable && (
        <button
          type="button"
          onClick={toggleMute}
          className="absolute top-3 right-3 z-10 flex items-center gap-2 rounded-lg bg-badge px-2.5 py-1.5 text-xs font-medium hover:bg-black/90"
        >
          <VolumeX size={14} />
          Started muted — click for sound
        </button>
      )}

      {countdown !== null && (
        <div className="absolute inset-0 z-20 grid place-items-center bg-black/75 px-6 text-center">
          <div>
            <p className="text-sm text-text-2">Up next in {countdown}</p>
            {nextVideoTitle && <p className="mt-1 clamp-2 text-base font-medium">{nextVideoTitle}</p>}
            <button
              type="button"
              onClick={() => {
                setCountdown(null)
                resetAutoplayChain()
              }}
              className="mt-4 rounded-full bg-surface px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {playable ? (
        <video
          ref={videoRef}
          src={stream?.url}
          className="h-full w-full cursor-pointer"
          playsInline
          preload="metadata"
          // Clicking the picture toggles playback, the way every video player
          // on the web behaves.
          onClick={toggle}
          onPlay={() => setPlaying(true)}
          onPause={() => setPlaying(false)}
          onVolumeChange={(e) => {
            setVolume(e.currentTarget.volume)
            setMuted(e.currentTarget.muted)
          }}
          onLoadedMetadata={(e) => {
            const element = e.currentTarget
            if (Number.isFinite(element.duration)) setDuration(element.duration)

            const resumeAt = resumeAtRef.current
            if (resumeAt > 0 && resumeAt < element.duration) {
              element.currentTime = resumeAt
            }

            // Start playing on arrival. If the browser refuses audible
            // autoplay, retry muted rather than leaving a dead frame, and
            // offer the unmute explicitly.
            element.play().catch(() => {
              element.muted = true
              setMuted(true)
              setAutoplayMuted(true)
              void element.play().catch(() => setAutoplayMuted(false))
            })
          }}
          onTimeUpdate={(e) => setPosition(e.currentTarget.currentTime)}
          onProgress={(e) => {
            const ranges = e.currentTarget.buffered
            if (ranges.length > 0) setBuffered(ranges.end(ranges.length - 1))
          }}
          onEnded={() => {
            setPlaying(false)
            if (!autoplayEnabled || !onPlayNext) return
            // Three hops with nobody touching anything means nobody is here.
            if (autoplayChainExhausted()) return
            setCountdown(5)
          }}
          onError={() => {
            if (retriedRef.current) {
              setLoadFailed(true)
              return
            }
            retriedRef.current = true
            void queryClient.invalidateQueries({ queryKey: ['stream', videoId] })
          }}
        >
          {captionsAvailable &&
            subtitles.map((track) => (
              <track
                key={track.language}
                kind="subtitles"
                src={track.url}
                srcLang={track.language}
                label={track.generated ? `${track.label} (auto)` : track.label}
              />
            ))}
        </video>
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
        <SeekBar
          position={position}
          duration={duration}
          buffered={buffered}
          disabled={!playable}
          onSeek={(next) => {
            setPosition(next)
            const element = videoRef.current
            if (element) element.currentTime = next
          }}
        />

        <div className="flex items-center gap-2 py-1.5 text-white">
          <button
            type="button"
            aria-label={playing ? 'Pause' : 'Play'}
            onClick={toggle}
            disabled={!playable}
            className="grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10"
          >
            {playing ? <Pause size={22} /> : <Play size={22} />}
          </button>

          <VolumeControl
            volume={muted ? 0 : volume}
            muted={muted}
            disabled={!playable}
            onToggleMute={toggleMute}
            onChange={applyVolume}
          />

          <span className="ml-1 text-xs tabular-nums">
            {formatDuration(position)} / {formatDuration(duration)}
          </span>
          <span className="flex-1" />

          {onPlayNext && (
            <button
              type="button"
              role="switch"
              aria-checked={autoplayEnabled}
              aria-label="Autoplay"
              title={autoplayEnabled ? 'Autoplay is on' : 'Autoplay is off'}
              onClick={() => setAutoplayEnabled(!autoplayEnabled)}
              className={
                'grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10 ' +
                (autoplayEnabled ? '' : 'text-text-2')
              }
            >
              <SkipForward size={22} />
            </button>
          )}

          {captionsAvailable && (
            <CaptionMenu tracks={subtitles} active={captions} onSelect={setCaptions} />
          )}

          <button
            type="button"
            aria-label="Full screen"
            onClick={() => void videoRef.current?.requestFullscreen?.()}
            disabled={!playable}
            className="grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10"
          >
            <Maximize size={22} />
          </button>
        </div>
      </div>
    </div>
  )
}

/**
 * Caption picker. Rendered only when tracks exist, so the control never appears
 * as something that does nothing — an upstream stream has no caption files.
 */
function CaptionMenu({
  tracks,
  active,
  onSelect,
}: {
  tracks: SubtitleTrack[]
  active: string | null
  onSelect: (language: string | null) => void
}) {
  const [open, setOpen] = useState(false)
  const Icon = active ? Captions : CaptionsOff

  return (
    <div className="relative">
      <button
        type="button"
        aria-label="Subtitles"
        aria-expanded={open}
        onClick={() => (tracks.length === 1 ? onSelect(active ? null : tracks[0].language) : setOpen((o) => !o))}
        className={
          'grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10 ' +
          (active ? 'border-b-2 border-white' : '')
        }
      >
        <Icon size={22} />
      </button>

      {open && tracks.length > 1 && (
        <ul className="absolute right-0 bottom-11 min-w-40 overflow-hidden rounded-lg bg-surface py-1 text-sm shadow-lg">
          <li>
            <button
              type="button"
              onClick={() => {
                onSelect(null)
                setOpen(false)
              }}
              className={
                'block w-full px-4 py-2 text-left transition-colors duration-150 ease-out hover:bg-surface-hover ' +
                (active === null ? 'font-medium' : '')
              }
            >
              Off
            </button>
          </li>
          {tracks.map((track) => (
            <li key={track.language}>
              <button
                type="button"
                onClick={() => {
                  onSelect(track.language)
                  setOpen(false)
                }}
                className={
                  'block w-full px-4 py-2 text-left transition-colors duration-150 ease-out hover:bg-surface-hover ' +
                  (active === track.language ? 'font-medium' : '')
                }
              >
                {track.label}
                {track.generated && <span className="text-text-2"> (auto)</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/**
 * Seek bar with a real buffered range behind the playhead. The grey band is
 * measured from the element, not simulated: when playback is running ahead of a
 * download, the viewer can see exactly how far they are able to skip.
 */
function SeekBar({
  position,
  duration,
  buffered,
  disabled,
  onSeek,
}: {
  position: number
  duration: number
  buffered: number
  disabled: boolean
  onSeek: (next: number) => void
}) {
  const safeDuration = Math.max(duration, 1)
  const playedPercent = (position / safeDuration) * 100
  const bufferedPercent = Math.min((buffered / safeDuration) * 100, 100)

  return (
    <div className="relative h-4">
      <div className="pointer-events-none absolute inset-x-0 top-1/2 h-1 -translate-y-1/2 rounded-full bg-white/25">
        <div className="h-full rounded-full bg-white/40" style={{ width: `${bufferedPercent}%` }} />
        <div
          className="absolute inset-y-0 left-0 rounded-full bg-brand"
          style={{ width: `${playedPercent}%` }}
        />
      </div>
      <input
        type="range"
        min={0}
        max={safeDuration}
        step={0.1}
        value={position}
        onChange={(e) => onSeek(Number(e.target.value))}
        disabled={disabled}
        aria-label="Seek"
        className="absolute inset-0 h-full w-full cursor-pointer opacity-0"
      />
    </div>
  )
}

/** Slider expands on hover or focus, as on youtube.com, to keep the bar compact. */
function VolumeControl({
  volume,
  muted,
  disabled,
  onToggleMute,
  onChange,
}: {
  volume: number
  muted: boolean
  disabled: boolean
  onToggleMute: () => void
  onChange: (next: number) => void
}) {
  const Icon = muted || volume === 0 ? VolumeX : volume < 0.5 ? Volume1 : Volume2

  return (
    <div className="group/volume flex items-center">
      <button
        type="button"
        aria-label={muted ? 'Unmute' : 'Mute'}
        onClick={onToggleMute}
        disabled={disabled}
        className="grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10"
      >
        <Icon size={22} />
      </button>
      <input
        type="range"
        min={0}
        max={1}
        step={0.05}
        value={volume}
        onChange={(e) => onChange(Number(e.target.value))}
        disabled={disabled}
        aria-label="Volume"
        className={
          'h-1 cursor-pointer accent-white transition-[width,opacity] duration-150 ease-out ' +
          'w-0 opacity-0 group-hover/volume:w-20 group-hover/volume:opacity-100 ' +
          'focus:w-20 focus:opacity-100'
        }
      />
    </div>
  )
}
