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
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { MediaState, SubtitleTrack } from '@/features/catalog/domain/video'
import type { StreamSources } from '@/features/catalog/infrastructure/catalogRepository'
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
/**
 * When the first progress report goes out.
 *
 * Long enough not to fire for a video someone opened and closed by accident,
 * short enough that skipping through a handful of tracks still leaves each of
 * them on record — which is what the ranker's "watched just now" penalty needs
 * in order to stop suggesting them straight back.
 */
const OPENING_REPORT_MS = 3_000
const SEEK_STEP_SECONDS = 5

/**
 * How far ahead of the playhead a replacement source is prepared.
 *
 * The new element is loaded and seeked to a mark slightly in the future, then
 * the swap happens when playback actually reaches that mark. Preparing at the
 * current position instead would mean jumping backwards by however long the
 * loading took — a second or two of already-watched video, which on music is
 * unmissable.
 */
const SWAP_LEAD_SECONDS = 0.6

type TierName = 'instant' | 'remux' | 'local'

interface Tier {
  name: TierName
  url: string
  seekable: boolean
  height?: number
}

/**
 * Picks the best available source.
 *
 * Order is not preference between equals: the local file is complete and
 * seekable, the instant upstream is seekable but low resolution, and the muxed
 * stream is full resolution but cannot be seeked at all and takes seconds to
 * produce its first frame. Anything seekable beats anything that is not.
 */
function pickTier(sources: StreamSources | undefined): Tier | undefined {
  if (!sources) return undefined
  if (sources.local) return { name: 'local', url: sources.local.url, seekable: true }
  if (sources.instant) {
    return {
      name: 'instant',
      url: sources.instant.url,
      seekable: true,
      height: sources.instant.height,
    }
  }
  if (sources.remux) return { name: 'remux', url: sources.remux.url, seekable: false }
  return undefined
}

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
  const { data: sources, isPending: resolvingStream, isError: streamFailed } = useStream(videoId)
  // Playing from upstream always schedules a copy, so a job is coming even if
  // the queue has not caught up yet.
  const download = useDownloadProgress(videoId, Boolean(sources) && !sources?.local)
  const queryClient = useQueryClient()

  // The best way to play right now. Local beats everything: it is on disk and
  // fully seekable. Otherwise the instant upstream, which starts in
  // milliseconds and seeks properly at the cost of resolution — and only when a
  // video publishes no progressive format at all does the muxed stream, slow to
  // start and unseekable, become the opening move.
  const tier = useMemo(() => pickTier(sources), [sources])

  // Two elements, so a change of source can be prepared out of sight and
  // switched to at a chosen moment. One element would have to drop what it is
  // showing in order to load the next thing, which is the black flash.
  const videoARef = useRef<HTMLVideoElement>(null)
  const videoBRef = useRef<HTMLVideoElement>(null)
  // Which element the viewer is watching. Kept in a ref as well as in state:
  // callbacks below are stable, and would otherwise close over a stale value.
  const [frontIsA, setFrontIsA] = useState(true)
  const frontIsARef = useRef(true)
  const front = useCallback(
    () => (frontIsARef.current ? videoARef.current : videoBRef.current),
    [],
  )
  const back = useCallback(
    () => (frontIsARef.current ? videoBRef.current : videoARef.current),
    [],
  )
  // URL loaded in each element. The back one is set only while an upgrade is
  // being prepared, and cleared afterwards so an abandoned stream is torn down
  // rather than left pulling bytes.
  const [srcA, setSrcA] = useState<string | undefined>(undefined)
  const [srcB, setSrcB] = useState<string | undefined>(undefined)
  const frontSrc = frontIsA ? srcA : srcB
  const setBackSrc = frontIsA ? setSrcB : setSrcA
  const [playing, setPlaying] = useState(false)
  const [volume, setVolume] = useState(1)
  const [muted, setMuted] = useState(false)
  const [position, setPosition] = useState(initialPositionSeconds)
  const [buffered, setBuffered] = useState(0)
  // Duration reported by the media element. Only meaningful once the whole
  // file is there; see `duration` below for why it is not used directly.
  const [elementDuration, setElementDuration] = useState(0)
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

  // Position to restore after the source swaps from upstream to the local copy.
  //
  // Written continuously from timeupdate, and frozen the moment the source
  // changes. The freeze is the whole point: changing <video src> resets
  // currentTime to 0, and the reset itself can dispatch a timeupdate — which
  // would otherwise overwrite the saved position with 0 and restart the video
  // from the beginning, exactly the bug this is here to prevent.
  const resumeAtRef = useRef(initialPositionSeconds)
  // The opening position for whichever video is current. Held in a ref so the
  // reset effect above can read the latest value without taking it as a
  // dependency; assigned during render, which runs before that effect.
  const initialPositionRef = useRef(initialPositionSeconds)
  initialPositionRef.current = initialPositionSeconds
  const swappingRef = useRef(true)
  // One re-resolve is allowed per mounted player. An expired upstream URL is
  // the common failure and it fixes itself; anything that survives a fresh URL
  // is a real failure and must be shown rather than retried forever.
  const retriedRef = useRef(false)
  // URL currently being prepared in the hidden element, or undefined when no
  // upgrade is in flight. Guards against starting the same swap twice.
  const upgradingToRef = useRef<string | undefined>(undefined)
  const handoverFrameRef = useRef(0)
  const justSwappedRef = useRef(false)

  // A fragmented stream declares no total length: its header says only how much
  // has been muxed so far, which grows as it plays. Trusting it makes the
  // progress bar read as full from the first second, since position and
  // duration are then the same number. The catalog knows the real length, so
  // that is what the bar is drawn against until the complete file takes over.
  const streaming = tier?.name === 'remux'
  const duration =
    !streaming && elementDuration > 0 ? elementDuration : durationSeconds

  const playable = Boolean(frontSrc) && !loadFailed
  // Captions no longer wait for the media file: ingest publishes them ahead of
  // the transfer, precisely so they are usable during upstream playback.
  const captionsAvailable = subtitles.length > 0

  // <track> elements are declarative but their display is not: the browser
  // decides which one shows. Driving textTracks directly keeps the button and
  // what is on screen in agreement.
  useEffect(() => {
    const element = front()
    if (!element) return
    for (let i = 0; i < element.textTracks.length; i++) {
      const track = element.textTracks[i]
      track.mode = track.language === captions ? 'showing' : 'disabled'
    }
  }, [captions, frontSrc, frontIsA, front, subtitles.length])

  // useLayoutEffect, not useEffect: this runs synchronously after React commits
  // the new src to the DOM and before the browser can dispatch any media event,
  // so the freeze is in place before a reset-to-zero timeupdate can land.
  useLayoutEffect(() => {
    // A handover is the one case where the front's source changes and there is
    // nothing to freeze: the element taking over is already loaded and already
    // sitting at the right position. Freezing here would leave the freeze on
    // for good, because the loadedmetadata that lifts it has long since fired.
    if (justSwappedRef.current) {
      justSwappedRef.current = false
      return
    }
    swappingRef.current = true
  }, [frontSrc])

  // Moving to another video keeps this component mounted — same route, new
  // param — so everything the refs and state hold about the old one has to be
  // put back by hand. useLayoutEffect for the same reason as the swap above:
  // it must land before the element can dispatch anything about the new source.
  //
  // Keyed on the video alone, deliberately. The starting position is an opening
  // value, not a live one, and treating it as a dependency made the player
  // reload itself whenever the catalogue row was refetched — which happens on
  // its own, because finishing a download invalidates that query and progress
  // is written back every fifteen seconds. The visible result is a video that
  // jumps to where it was last saved, part way through watching it.
  useLayoutEffect(() => {
    retriedRef.current = false
    setLoadFailed(false)
    // Otherwise the previous video's length would draw this one's progress bar
    // until the element got around to reporting its own.
    setElementDuration(0)
    setBuffered(0)
    // Carrying this over would seek the new video to wherever the previous one
    // was left, which is not a position that means anything here.
    resumeAtRef.current = initialPositionRef.current
    setPosition(initialPositionRef.current)
    // Both elements start empty, and the front one goes back to being A, so a
    // new video never inherits the previous one's half-prepared upgrade.
    setSrcA(undefined)
    setSrcB(undefined)
    frontIsARef.current = true
    setFrontIsA(true)
    upgradingToRef.current = undefined
  }, [videoId])

  // Load the opening source, and afterwards prepare any better one out of sight.
  //
  // This is the whole of the tier machinery: the front element is never asked
  // to change what it is playing, because that is precisely what makes the
  // picture drop out. A replacement is loaded into the hidden element instead,
  // and the two are exchanged once the replacement is genuinely ready.
  useEffect(() => {
    if (!tier) return

    // Nothing playing yet: this is the first source, so it goes straight to the
    // front. There is no picture to protect.
    if (!frontSrc) {
      if (frontIsARef.current) setSrcA(tier.url)
      else setSrcB(tier.url)
      return
    }

    if (tier.url === frontSrc) return
    // An upgrade to this source is already being prepared.
    if (upgradingToRef.current === tier.url) return

    upgradingToRef.current = tier.url
    setBackSrc(tier.url)
  }, [tier, frontSrc, setBackSrc])

  // Give up on an upgrade and keep playing what already works. Failing to
  // prepare a better source is not a playback failure — nothing on screen
  // changes — so it must never surface as one.
  const abandonUpgrade = useCallback(() => {
    upgradingToRef.current = undefined
    window.cancelAnimationFrame(handoverFrameRef.current)
    if (frontIsARef.current) setSrcB(undefined)
    else setSrcA(undefined)
  }, [])

  // Hand over to the prepared element once it can actually play.
  //
  // The mark is a moment slightly ahead of the playhead: the replacement is
  // seeked there and waits, and the exchange happens when playback arrives. So
  // the viewer never sees a repeated second, and never sees a gap either.
  const handoverToBack = useCallback(() => {
    const current = front()
    const next = back()
    if (!current || !next || !upgradingToRef.current) return

    const commit = () => {
      // Carry across everything the viewer set, or the swap would silently undo
      // their volume, their mute and their subtitles.
      next.volume = current.volume
      next.muted = current.muted
      for (let i = 0; i < next.textTracks.length; i++) {
        const track = next.textTracks[i]
        track.mode = track.language === captions ? 'showing' : 'disabled'
      }

      // Freeze position tracking across the exchange for the same reason a
      // source change freezes it: the element being left behind will report
      // times that no longer mean anything.
      swappingRef.current = true
      resumeAtRef.current = next.currentTime

      const wasPlaying = !current.paused
      justSwappedRef.current = true
      frontIsARef.current = !frontIsARef.current
      setFrontIsA(frontIsARef.current)
      upgradingToRef.current = undefined

      if (wasPlaying) void next.play().catch(() => undefined)
      current.pause()
      // Dropping the old source releases it — for the muxed stream that is what
      // kills the ffmpeg process still muxing the rest of the video.
      if (frontIsARef.current) setSrcB(undefined)
      else setSrcA(undefined)
      swappingRef.current = false
    }

    // Paused: no mark to wait for, so exchange where the viewer is.
    if (current.paused) {
      commit()
      return
    }

    // Wait for the playhead to reach the mark the replacement is parked on.
    // requestAnimationFrame rather than timeupdate, which fires only about four
    // times a second — coarse enough to overshoot the mark and jump backwards.
    const waitForMark = () => {
      if (upgradingToRef.current === undefined) return
      if (current.currentTime >= next.currentTime - 0.05) {
        commit()
        return
      }
      handoverFrameRef.current = window.requestAnimationFrame(waitForMark)
    }
    waitForMark()
  }, [front, back, captions])

  // A pending handover must not outlive the video it belongs to.
  useEffect(() => () => window.cancelAnimationFrame(handoverFrameRef.current), [])

  // Kept in a ref because the progress reporter runs on a timer, from an effect
  // that must not be torn down and rebuilt every time the duration is refined.
  const trustedDurationRef = useRef(durationSeconds)
  useEffect(() => {
    trustedDurationRef.current = duration
  }, [duration])

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
    const element = front()
    if (!element) return
    if (element.paused) void element.play()
    else element.pause()
  }, [])

  const seekBy = useCallback((delta: number) => {
    resetAutoplayChain()
    const element = front()
    if (!element) return
    element.currentTime = Math.max(0, Math.min(element.duration || 0, element.currentTime + delta))
  }, [])

  const applyVolume = (next: number) => {
    resetAutoplayChain()
    const element = front()
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
    const element = front()
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
      const element = front()
      if (!element) return

      // The catalogue's duration, not the element's.
      //
      // A muxed-on-the-fly stream reports only how much has been assembled so
      // far, and that number grows as it plays — so dividing by it says the
      // viewer is nearly finished from the first second onwards. Measured on
      // this library: a 243-second video watched to 0:41 was recorded as 92%
      // complete. Ranking treats watch ratio as the signal that a video was
      // worth opening, so that one division was quietly telling it that
      // everything abandoned early was excellent.
      const total = trustedDurationRef.current
      if (!total) return

      void repo
        .recordProgress(
          videoId,
          Math.floor(element.currentTime),
          Math.min(1, Math.max(0, element.currentTime / total)),
        )
        .catch(() => {
          // Losing a progress ping degrades ranking slightly; never surface it.
        })
    }

    // One report shortly after playback starts, before the interval takes over.
    //
    // Ranking demotes anything watched in the last few hours, which is what
    // stops "next" walking in a circle between two videos that are each other's
    // best match. That guard cannot fire on a video the server has never been
    // told was watched — and with only a fifteen-second timer, skipping quickly
    // through a few videos left no trace of any of them, so every one stayed a
    // perfectly good suggestion to come back to.
    const opener = window.setTimeout(report, OPENING_REPORT_MS)
    const timer = window.setInterval(report, PROGRESS_INTERVAL_MS)
    return () => {
      window.clearTimeout(opener)
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
      {tier && tier.name !== 'local' && (
        <div className="absolute top-3 left-3 z-10 flex items-center gap-2 rounded-lg bg-badge px-2.5 py-1.5 text-xs font-medium">
          {/* States the resolution actually on screen, because it is about to
              change: the opening source is deliberately a low one, and the
              downloaded file replaces it mid-playback. A viewer who sees a soft
              picture should be able to tell that it is temporary. */}
          <span
            title={
              tier.name === 'instant'
                ? 'Playing upstream while the full-quality copy downloads'
                : 'Muxed live — seeking needs the downloaded file'
            }
          >
            {tier.name === 'instant' ? `${tier.height ?? 360}p` : 'Live'}
          </span>
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
        // Two layers, permanently mounted. Only one is ever visible; the other
        // is where a better source is quietly loaded and lined up. Both stay in
        // the tree so that exchanging them is a change of opacity rather than a
        // teardown, which is what keeps the picture from blinking.
        <>
          {([true, false] as const).map((isA) => {
            const src = isA ? srcA : srcB
            const isFront = isA === frontIsA
            return (
              <video
                key={isA ? 'a' : 'b'}
                ref={isA ? videoARef : videoBRef}
                src={src}
                className="absolute inset-0 h-full w-full cursor-pointer"
                // The hidden layer must stay laid out and decoding — display:none
                // would stop it buffering, which is the entire point of it.
                style={{ opacity: isFront ? 1 : 0, pointerEvents: isFront ? undefined : 'none' }}
                aria-hidden={!isFront}
                playsInline
                // The layer being prepared has to buffer ahead of being needed;
                // metadata alone would leave it unable to take over.
                preload={isFront ? 'metadata' : 'auto'}
                // Clicking the picture toggles playback, the way every video
                // player on the web behaves.
                onClick={isFront ? toggle : undefined}
                onPlay={isFront ? () => setPlaying(true) : undefined}
                onPause={isFront ? () => setPlaying(false) : undefined}
                onVolumeChange={
                  isFront
                    ? (e) => {
                        setVolume(e.currentTarget.volume)
                        setMuted(e.currentTarget.muted)
                      }
                    : undefined
                }
                onLoadedMetadata={(e) => {
                  const element = e.currentTarget
                  if (!isFront) {
                    // Park the replacement a moment ahead of the playhead and
                    // wait there. Seeking is what makes the exchange seamless,
                    // so a source that cannot seek hands over immediately.
                    const current = front()
                    const mark =
                      current && !current.paused
                        ? current.currentTime + SWAP_LEAD_SECONDS
                        : (current?.currentTime ?? 0)

                    if (mark <= 0) {
                      // Nothing has played yet, so both elements already agree
                      // on where they are.
                      handoverToBack()
                    } else if (Number.isFinite(element.duration) && mark < element.duration) {
                      element.currentTime = mark
                    } else {
                      // The mark is past the end of the video. Handing over now
                      // would swap in an element still sitting at zero and throw
                      // the viewer back to the start; there is nothing worth
                      // upgrading this close to the end anyway.
                      abandonUpgrade()
                    }
                    return
                  }

                  if (Number.isFinite(element.duration)) setElementDuration(element.duration)

                  const resumeAt = resumeAtRef.current
                  if (resumeAt > 0 && resumeAt < element.duration) {
                    element.currentTime = resumeAt
                  }
                  // The new source is loaded and positioned: resume tracking.
                  swappingRef.current = false

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
                // Only meaningful on the layer being prepared: it means the
                // replacement has reached its mark and can take over.
                onSeeked={isFront ? undefined : () => handoverToBack()}
                onDurationChange={
                  isFront
                    ? (e) => {
                        const value = e.currentTarget.duration
                        if (Number.isFinite(value) && value > 0) setElementDuration(value)
                      }
                    : undefined
                }
                onTimeUpdate={
                  isFront
                    ? (e) => {
                        setPosition(e.currentTarget.currentTime)
                        // Frozen across a source swap, so the browser's reset to
                        // 0 cannot erase where the viewer actually was.
                        if (!swappingRef.current) {
                          resumeAtRef.current = e.currentTarget.currentTime
                        }
                      }
                    : undefined
                }
                onProgress={
                  isFront
                    ? (e) => {
                        const ranges = e.currentTarget.buffered
                        if (ranges.length > 0) setBuffered(ranges.end(ranges.length - 1))
                      }
                    : undefined
                }
                onEnded={
                  isFront
                    ? () => {
                        setPlaying(false)
                        if (!autoplayEnabled || !onPlayNext) return
                        // Three hops with nobody touching anything means nobody
                        // is here.
                        if (autoplayChainExhausted()) return
                        setCountdown(5)
                      }
                    : undefined
                }
                onError={() => {
                  if (!isFront) {
                    // An upgrade that will not load is not a failure worth
                    // showing: what is on screen still works. Abandon it.
                    abandonUpgrade()
                    return
                  }
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
            )
          })}
        </>
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
          // Seeking works from the first second now: the opening source is a
          // progressive file the browser can range-request. Only the muxed
          // stream — the fallback for videos publishing no progressive format
          // at all — has no index and cannot be seeked.
          disabled={!playable || !tier?.seekable}
          onSeek={(next) => {
            setPosition(next)
            const element = front()
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

          {/* A real Next button. The autoplay switch sits further along the bar:
              a skip-forward icon means "go to the next video" everywhere else,
              so using it for a toggle made the control look broken. */}
          {onPlayNext && (
            <button
              type="button"
              aria-label="Next video"
              title={nextVideoTitle ? `Next: ${nextVideoTitle}` : 'Next video'}
              onClick={() => {
                resetAutoplayChain()
                setCountdown(null)
                onPlayNext()
              }}
              className="grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10"
            >
              <SkipForward size={22} />
            </button>
          )}

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
              className="grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10"
            >
              {/* Drawn as a switch, not an action: the state has to be readable
                  at a glance, and an icon that looks clickable-once would read
                  as "skip", which is the button next to it. */}
              <span
                className={
                  'flex h-3.5 w-7 items-center rounded-full px-0.5 transition-colors duration-150 ease-out ' +
                  (autoplayEnabled ? 'bg-white' : 'bg-white/30')
                }
              >
                <span
                  className={
                    'h-2.5 w-2.5 rounded-full transition-transform duration-150 ease-out ' +
                    (autoplayEnabled ? 'translate-x-3.5 bg-black' : 'bg-white')
                  }
                />
              </span>
            </button>
          )}

          {captionsAvailable && (
            <CaptionMenu tracks={subtitles} active={captions} onSelect={setCaptions} />
          )}

          <button
            type="button"
            aria-label="Full screen"
            onClick={() => void front()?.requestFullscreen?.()}
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
