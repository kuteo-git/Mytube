import clsx from 'clsx'
import {
  Captions,
  CaptionsOff,
  Maximize,
  Pause,
  Play,
  Settings,
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
import type { QualityChoice } from '@/features/watch/application/autoplay'
import {
  autoplayChainExhausted,
  resetAutoplayChain,
  useAutoplayPreference,
  useQualityPreference,
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

/**
 * How far ahead of the playhead a muxed stream is opened.
 *
 * It begins wherever it is asked to and takes a while to get there, so the mark
 * has to be further ahead than the preparation takes — otherwise playback has
 * already passed it by the time the stream is ready, and the handover either
 * rewinds or never matches.
 *
 * How long it takes depends on the video, which is what made the first guess
 * wrong. Measured on this library: about 4.4s for a five-minute video, but
 * 10.8s for a seventy-eight-minute one — 2.8s of that resolving and the rest
 * inside ffmpeg. Twenty seconds clears the longest case measured with room to
 * spare, at the cost of the picture staying low for that long.
 */
const REMUX_PREPARE_LEAD_SECONDS = 20

/**
 * How long a muxed stream may take to become playable before auto gives up.
 *
 * Past this it is not going to keep up with playback either, and a smooth low
 * rendition beats a stuttering high one. Only auto gives up; a viewer who
 * pinned 1080p asked for it and keeps it.
 */
const REMUX_PATIENCE_MS = 45_000

/**
 * How far past the mark a handover may still happen.
 *
 * Beyond this the replacement is behind the picture and swapping to it would
 * visibly rewind. Small, because a second of repeated video is already
 * noticeable on music.
 */
const HANDOVER_OVERSHOOT_TOLERANCE = 1

/**
 * How long the controls stay up after the pointer stops moving.
 *
 * Three seconds is what youtube.com uses and what people therefore expect. Long
 * enough to travel from the picture to the seek bar without it vanishing on the
 * way, short enough that it does not sit over a film.
 */
const CONTROLS_IDLE_MS = 3000

/**
 * How many times auto will try the muxed stream before settling for the low
 * rendition.
 *
 * More than one because a single aborted fetch is not evidence the connection
 * cannot carry it; few, because one that genuinely cannot will fail every time
 * and each attempt spends seconds muxing a stream nobody ends up watching.
 */
const MAX_REMUX_ATTEMPTS = 3

/**
 * The height the full-quality tiers are labelled with when they do not say.
 *
 * The local file and the muxed stream are both produced at the configured
 * download height, and neither carries it in the stream answer.
 */
const remuxLabelHeight = 1080

type TierName = 'instant' | 'remux' | 'local'

interface Tier {
  name: TierName
  /** Without any offset. `sourceURL` below adds one for the muxed stream. */
  url: string
  /** True when the browser can seek within it by itself. */
  seekable: boolean
  height?: number
}

/**
 * Where the video actually starts, for a tier opened at an offset.
 *
 * Only the muxed stream has one. It carries no index, so seeking means asking
 * for a fresh mux beginning at the mark — and the element then believes it is
 * at zero. Everything the viewer sees has to add this back, or a video seeked
 * to ten minutes reports itself as just beginning.
 */
function sourceURL(tier: Tier, offsetSeconds: number): string {
  if (tier.name !== 'remux' || offsetSeconds <= 0) return tier.url
  return `${tier.url}?t=${offsetSeconds.toFixed(3)}`
}

/**
 * Every way this video can be played, best first.
 *
 * Order is not preference between equals. The local file is complete and
 * seekable; the muxed stream is the same resolution but has no index, so
 * seeking it means reopening it; the instant upstream seeks freely but is
 * whatever low rendition YouTube still publishes muxed.
 */
function availableTiers(sources: StreamSources | undefined): Tier[] {
  if (!sources) return []
  const tiers: Tier[] = []
  if (sources.local) {
    tiers.push({ name: 'local', url: sources.local.url, seekable: true })
  }
  if (sources.remux) {
    tiers.push({
      name: 'remux',
      url: sources.remux.url,
      seekable: false,
      height: sources.remux.height,
    })
  }
  if (sources.instant) {
    tiers.push({
      name: 'instant',
      url: sources.instant.url,
      seekable: true,
      height: sources.instant.height,
    })
  }
  return tiers
}

/**
 * The tier to open first.
 *
 * Always the one that starts fastest, which is the instant upstream — the whole
 * point of the design is that pressing play produces a picture immediately and
 * the quality arrives afterwards. The muxed stream is the opening move only for
 * videos that publish no progressive rendition at all, where there is nothing
 * faster to fall back to.
 */
function openingTier(tiers: Tier[], choice: QualityChoice): Tier | undefined {
  if (tiers.length === 0) return undefined
  const local = tiers.find((t) => t.name === 'local')
  if (local) return local
  if (choice === 'high') {
    return tiers.find((t) => t.name === 'remux') ?? tiers[0]
  }
  return tiers.find((t) => t.name === 'instant') ?? tiers[0]
}

/**
 * The tier the player should be moving towards, or undefined when it is already
 * on the best one available.
 *
 * Pinning is a command, not a preference: a viewer who picked 360p keeps it
 * even once the download lands, and one who picked 1080p keeps it even if the
 * connection is struggling. Only "auto" climbs, and only "auto" retreats.
 */
function targetTier(
  tiers: Tier[], current: TierName | undefined, choice: QualityChoice, remuxFailed: boolean,
): Tier | undefined {
  if (tiers.length === 0) return undefined

  let wanted: Tier | undefined
  switch (choice) {
    case 'low':
      wanted = tiers.find((t) => t.name === 'instant') ?? tiers.find((t) => t.name === 'local')
      break
    case 'high':
      wanted = tiers.find((t) => t.name === 'local') ?? tiers.find((t) => t.name === 'remux')
      break
    default:
      // Auto: the local file whenever it exists, otherwise full resolution
      // muxed live — unless that has already been tried and could not keep up,
      // in which case a smooth low rendition beats a stuttering high one.
      wanted =
        tiers.find((t) => t.name === 'local') ??
        (remuxFailed ? undefined : tiers.find((t) => t.name === 'remux')) ??
        tiers.find((t) => t.name === 'instant')
  }

  if (!wanted || wanted.name === current) return undefined
  return wanted
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

  const tiers = useMemo(() => availableTiers(sources), [sources])
  const [quality, setQuality] = useQualityPreference()
  // How many times the muxed stream has been attempted and abandoned.
  //
  // One failure used to be final, and that was too brittle by half: a single
  // aborted fetch — observed as a connection the browser dropped 115ms after
  // the server began writing, having read nothing — turned the tier off for the
  // rest of the video. Transient failures are exactly what a retry is for. The
  // limit exists because a connection that genuinely cannot sustain the mux
  // will fail every time, and each attempt costs seconds of a stream nobody
  // watches.
  const [remuxAttempts, setRemuxAttempts] = useState(0)
  const remuxFailed = remuxAttempts >= MAX_REMUX_ATTEMPTS
  // Which tier the front element is playing, and where in the video that
  // element's zero is. Only the muxed stream has a non-zero offset.
  const [tier, setTier] = useState<Tier | undefined>(undefined)
  const [offsetSeconds, setOffsetSeconds] = useState(0)
  const offsetRef = useRef(0)
  offsetRef.current = offsetSeconds
  // Read by seekTo, which is a stable callback and would otherwise close over
  // whichever tier was current when it was created.
  const tierRef = useRef<Tier | undefined>(undefined)
  tierRef.current = tier
  // True while a muxed stream is being reopened at a new mark. The picture is
  // still the old one, so this is what tells the viewer the seek was heard.
  const [seeking, setSeeking] = useState(false)

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
  const backSrc = frontIsA ? srcB : srcA
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
  // Language code of the active caption track, or null for off. Tracks arrive
  // shortly after playback starts, before the media file finishes downloading.
  const [captions, setCaptions] = useState<string | null>(null)
  const [autoplayEnabled, setAutoplayEnabled] = useAutoplayPreference()
  // Seconds left before the next video starts, or null when no countdown runs.
  const [countdown, setCountdown] = useState<number | null>(null)

  // Whether the chrome is on screen.
  //
  // Hidden while a video plays undisturbed, as on youtube.com: the controls are
  // there to be used, and a bar across the picture the whole time is a bar
  // across the picture the whole time.
  const [pointerActive, setPointerActive] = useState(true)
  // Menus keep the chrome up on their own. A viewer who opens the quality menu
  // and then stops moving the mouse must not have it taken away mid-decision.
  const [openMenus, setOpenMenus] = useState(0)
  const hideTimerRef = useRef(0)

  // Menus report opening and closing so the chrome can stay up while one is
  // in use. Counted rather than a boolean: two menus can be mounted, and the
  // second closing must not clear a flag the first still needs.
  const trackMenu = useCallback((open: boolean) => {
    setOpenMenus((count) => Math.max(0, count + (open ? 1 : -1)))
  }, [])

  const wakeControls = useCallback(() => {
    setPointerActive(true)
    window.clearTimeout(hideTimerRef.current)
    hideTimerRef.current = window.setTimeout(
      () => setPointerActive(false),
      CONTROLS_IDLE_MS,
    )
  }, [])

  // Paused counts as attention: nothing is being obscured, and the controls are
  // the only thing on screen worth looking at.
  const controlsVisible = pointerActive || !playing || openMenus > 0

  useEffect(() => () => window.clearTimeout(hideTimerRef.current), [])

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
  // The tier the hidden element is preparing, and the offset it was opened at.
  // Applied to the visible state only once the handover actually happens.
  const pendingTierRef = useRef<{ tier: Tier; offset: number } | undefined>(undefined)
  // Absolute position in the video, offset included. Read by the effect that
  // opens a muxed stream, which needs to know where the viewer is without
  // taking position as a dependency and restarting on every tick.
  const positionRef = useRef(0)
  const handoverFrameRef = useRef(0)
  // True while sound is off only because the browser insisted, as opposed to
  // because the viewer turned it off. The distinction is the whole point: one
  // should be undone at the first opportunity, the other never.
  const mutedByPolicyRef = useRef(false)
  const justSwappedRef = useRef(false)

  // A fragmented stream declares no total length: its header says only how much
  // has been muxed so far, which grows as it plays. Trusting it makes the
  // progress bar read as full from the first second, since position and
  // duration are then the same number. The catalog knows the real length, so
  // that is what the bar is drawn against until the complete file takes over.
  const streaming = tier?.name === 'remux'
  // The catalogue's length is the fallback and, for a stream opened partway
  // through, the only honest answer: an element that starts at ten minutes
  // reports only what remains, and a bar drawn against that would say a
  // half-watched film is barely begun.
  const duration =
    !streaming && offsetSeconds === 0 && elementDuration > 0 ? elementDuration : durationSeconds

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
    // Without this the guard in the upgrade effect would still be holding a
    // tier from the previous video and block this one's first climb.
    pendingTierRef.current = undefined
    setTier(undefined)
    setOffsetSeconds(0)
    offsetRef.current = 0
    positionRef.current = initialPositionRef.current
    setRemuxAttempts(0)
    setSeeking(false)
  }, [videoId])

  // Load the opening source, and afterwards prepare any better one out of sight.
  //
  // This is the whole of the tier machinery: the front element is never asked
  // to change what it is playing, because that is precisely what makes the
  // picture drop out. A replacement is loaded into the hidden element instead,
  // and the two are exchanged once the replacement is genuinely ready.
  useEffect(() => {
    // Nothing playing yet: open the fastest tier straight into the front
    // element. There is no picture to protect, and the point of the design is
    // that this happens immediately.
    if (!frontSrc) {
      const opening = openingTier(tiers, quality)
      if (!opening) return
      setTier(opening)
      setOffsetSeconds(0)
      if (frontIsARef.current) setSrcA(sourceURL(opening, 0))
      else setSrcB(sourceURL(opening, 0))
      return
    }

    const wanted = targetTier(tiers, tier?.name, quality, remuxFailed)
    if (!wanted) return

    // The muxed stream has to be opened where the viewer already is, and a
    // little ahead of it: the mux takes a couple of seconds to produce its
    // first fragment, and opening at the current position would mean handing
    // over to an element that starts before where playback has since reached.
    // One attempt per tier at a time, keyed on the tier rather than the URL.
    //
    // The URL for a muxed stream carries the mark it opens at, and that mark
    // moves as the video plays. The stream answer is also re-polled every few
    // seconds while there is no local file, which re-runs this effect. Keyed on
    // the URL, every poll therefore computed a later mark, saw a different
    // string, and started the mux again — discarding one that was seconds from
    // being ready, forever. The visible symptom is a video that never leaves
    // the low rendition however long it is left alone.
    if (pendingTierRef.current?.tier.name === wanted.name) return

    const startAt =
      wanted.name === 'remux' ? positionRef.current + REMUX_PREPARE_LEAD_SECONDS : 0
    const url = sourceURL(wanted, startAt)
    if (upgradingToRef.current === url) return

    upgradingToRef.current = url
    pendingTierRef.current = { tier: wanted, offset: wanted.name === 'remux' ? startAt : 0 }
    setBackSrc(url)
  }, [tiers, tier, quality, remuxFailed, frontSrc, setBackSrc])

  // Give up on an upgrade and keep playing what already works. Failing to
  // prepare a better source is not a playback failure — nothing on screen
  // changes — so it must never surface as one.
  const abandonUpgrade = useCallback(() => {
    // A muxed stream that could not be prepared is not tried again in auto: the
    // connection that failed it will not have changed, and a second attempt is
    // more seconds of stalling to learn the same thing. A viewer who pinned
    // 1080p is not overruled — targetTier ignores this for them.
    if (pendingTierRef.current?.tier.name === 'remux') setRemuxAttempts((n) => n + 1)
    pendingTierRef.current = undefined
    setSeeking(false)
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

      // The incoming tier's frame becomes the current one, and only now — a
      // handover that never happened must not leave the offset pointing at a
      // stream nobody is watching.
      const pending = pendingTierRef.current
      const nextOffset = pending?.offset ?? 0

      // Take the incoming element's length with it.
      //
      // A handover changes no src, so the element arriving at the front never
      // fires loadedmetadata again and elementDuration keeps whatever the last
      // tier reported. Swapping from the muxed stream — whose length is only
      // however much has been assembled so far — to the finished file therefore
      // left the seek bar drawn against a few seconds of video instead of the
      // whole thing.
      setElementDuration(Number.isFinite(next.duration) ? next.duration : 0)
      resumeAtRef.current = next.currentTime + nextOffset
      positionRef.current = resumeAtRef.current
      offsetRef.current = nextOffset
      setOffsetSeconds(nextOffset)
      if (pending) setTier(pending.tier)
      pendingTierRef.current = undefined
      setSeeking(false)

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
    // Compared in absolute time, because the two elements need not share a
    // frame: a muxed stream opened at ten minutes calls that moment zero. The
    // old comparison of raw currentTimes was only ever right when both offsets
    // were zero, which stopped being true the moment a third tier existed.
    const waitForMark = () => {
      if (upgradingToRef.current === undefined) return
      const backAbsolute = (pendingTierRef.current?.offset ?? 0) + next.currentTime
      const frontAbsolute = current.currentTime + offsetRef.current
      if (frontAbsolute >= backAbsolute - 0.05) {
        // Overshooting the mark by more than a moment means the replacement
        // took longer to prepare than it was given, and handing over would
        // rewind the viewer by the difference. Better to keep what is playing:
        // a picture that stays low is a disappointment, one that jumps
        // backwards is a fault.
        if (frontAbsolute > backAbsolute + HANDOVER_OVERSHOOT_TOLERANCE) {
          abandonUpgrade()
          return
        }
        commit()
        return
      }
      handoverFrameRef.current = window.requestAnimationFrame(waitForMark)
    }
    waitForMark()
  }, [front, back, captions, abandonUpgrade])

  // Give the sound back at the first gesture the document gets.
  //
  // Any click or key press satisfies the autoplay policy, so there is no need
  // to ask for one — the next thing the viewer does for their own reasons is
  // enough. Only unmutes what the policy muted; someone who reached for the
  // mute button keeps their silence.
  useEffect(() => {
    const restore = () => {
      if (!mutedByPolicyRef.current) return
      mutedByPolicyRef.current = false
      const element = front()
      if (!element) return
      element.muted = false
      setMuted(false)
    }
    window.addEventListener('pointerdown', restore)
    window.addEventListener('keydown', restore)
    return () => {
      window.removeEventListener('pointerdown', restore)
      window.removeEventListener('keydown', restore)
    }
  }, [front])

  // A pending handover must not outlive the video it belongs to.
  useEffect(() => () => window.cancelAnimationFrame(handoverFrameRef.current), [])

  // Give up on a muxed stream that is taking too long to become playable.
  //
  // Without this the rail waits indefinitely on a connection that cannot
  // sustain the mux, and the viewer sits on the low rendition with no
  // indication that anything is being attempted or has failed. Only auto gives
  // up; a viewer who pinned 1080p asked for it.
  useEffect(() => {
    if (!backSrc || quality === 'high') return
    const timer = window.setTimeout(() => {
      if (pendingTierRef.current?.tier.name === 'remux') abandonUpgrade()
    }, REMUX_PATIENCE_MS)
    return () => window.clearTimeout(timer)
  }, [backSrc, quality, abandonUpgrade])

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

  /**
   * Moves to an absolute position, however the current tier allows it.
   *
   * A seekable tier is a plain assignment. The muxed stream has no index, so
   * the only way to move within it is to open a new one starting there — the
   * picture is replaced rather than repositioned, which costs a couple of
   * seconds and is why this is never called while a scrub handle is being
   * dragged.
   */
  const seekTo = useCallback(
    (absolute: number) => {
      resetAutoplayChain()
      const element = front()
      if (!element) return

      const target = Math.max(0, absolute)
      setPosition(target)
      positionRef.current = target
      resumeAtRef.current = target

      if (tierRef.current?.seekable !== false) {
        element.currentTime = Math.max(0, target - offsetRef.current)
        return
      }

      const currentTier = tierRef.current
      if (!currentTier) return
      // Reopening is a source change, so it goes through the same hidden-element
      // handover as everything else: the picture only leaves once the
      // replacement can actually play.
      const url = sourceURL(currentTier, target)
      if (upgradingToRef.current === url) return
      upgradingToRef.current = url
      pendingTierRef.current = { tier: currentTier, offset: target }
      setSeeking(true)
      if (frontIsARef.current) setSrcB(url)
      else setSrcA(url)
    },
    [front],
  )

  const seekBy = useCallback(
    (delta: number) => {
      seekTo(positionRef.current + delta)
    },
    [seekTo],
  )

  const applyVolume = (next: number) => {
    resetAutoplayChain()
    // Reaching for the volume is a decision about sound, so the automatic
    // restore steps aside for the same reason it does for the mute button.
    mutedByPolicyRef.current = false
    const element = front()
    setVolume(next)
    if (element) {
      element.volume = next
      element.muted = next === 0
      setMuted(next === 0)
    }
    // Touching the volume is a gesture, so audible playback is allowed again.
  }

  const toggleMute = () => {
    const element = front()
    if (!element) return
    // A deliberate choice, so the automatic restore must not overrule it.
    mutedByPolicyRef.current = false
    element.muted = !element.muted
    setMuted(element.muted)
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

  // Stop the transfer when this video is left.
  //
  // Pressing play schedules a copy so the video is on disk next time. But a
  // copy nobody is waiting for is a request to YouTube nobody is waiting for
  // either, and this address has already been blocked once for making too many
  // of those — with the block taking out stream resolution as well, so nothing
  // outside the already-downloaded files would play at all.
  //
  // Cleanup rather than a leave handler, so it covers both directions of
  // leaving: moving to the next video, and closing the page.
  useEffect(() => {
    if (!videoId) return
    return () => {
      void repo.cancelDownload(videoId).catch(() => {
        // A transfer that outlives its viewer is waste, not breakage. Failing
        // to stop it must not surface anywhere.
      })
    }
  }, [videoId])

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

  // What is on screen, named the way a viewer would name it.
  const tierLabel = (() => {
    if (!tier) return ''
    if (tier.name === 'local') return `${remuxLabelHeight}p`
    if (tier.name === 'remux') return `${tier.height ?? remuxLabelHeight}p`
    return `${tier.height ?? 360}p`
  })()

  // Only the choices this video can actually honour. A menu entry that cannot
  // be delivered is worse than one that is missing.
  const qualityOptions = useMemo(() => {
    const options: { value: QualityChoice; label: string }[] = [{ value: 'auto', label: 'Auto' }]
    const high = tiers.find((t) => t.name === 'local' || t.name === 'remux')
    if (high) options.push({ value: 'high', label: `${high.height ?? remuxLabelHeight}p` })
    const low = tiers.find((t) => t.name === 'instant')
    if (low) options.push({ value: 'low', label: `${low.height ?? 360}p` })
    return options
  }, [tiers])

  const downloading = download?.state === 'RUNNING' || download?.state === 'QUEUED'
  const downloadPercent = Math.round((download?.progress ?? 0) * 100)

  return (
    <div
      className={clsx(
        'group/player relative aspect-video w-full overflow-hidden rounded-xl bg-black',
        // The cursor goes with the chrome. Leaving an arrow sitting on a film is
        // the same distraction in miniature.
        !controlsVisible && 'cursor-none',
      )}
      style={
        playable
          ? undefined
          : { background: `radial-gradient(120% 90% at 50% 30%, hsl(${hue} 40% 22%), #000 70%)` }
      }
      onPointerMove={wakeControls}
      onPointerDown={wakeControls}
      // Leaving takes the chrome immediately rather than after the delay: the
      // pointer is demonstrably elsewhere, so there is nothing to wait for.
      onPointerLeave={() => {
        window.clearTimeout(hideTimerRef.current)
        setPointerActive(false)
      }}
    >
      {tier && tier.name !== 'local' && (
        <div
          className={clsx(
            'absolute top-3 left-3 z-10 flex items-center gap-2 rounded-lg bg-badge px-2.5 py-1.5 text-xs font-medium',
            'transition-opacity duration-200 ease-out',
            controlsVisible ? 'opacity-100' : 'opacity-0',
          )}
        >
          {/* States the resolution actually on screen, because it is about to
              change: the opening source is deliberately a low one, and the
              downloaded file replaces it mid-playback. A viewer who sees a soft
              picture should be able to tell that it is temporary. */}
          <span
            title={
              tier.name === 'instant'
                ? 'Playing upstream while a better source is prepared'
                : 'Muxed live — seeking reopens the stream, so it takes a moment'
            }
          >
            {tierLabel}
            {tier.name === 'remux' && ' · live'}
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

      {/* Reopening a muxed stream at a new mark takes a couple of seconds, and
          the old picture stays on screen throughout. Without this the seek
          looks like it was ignored. */}
      {seeking && (
        <div className="absolute inset-0 z-10 grid place-items-center bg-black/40">
          <span className="rounded-lg bg-badge px-3 py-2 text-sm font-medium">Seeking…</span>
        </div>
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
                    // The mark is absolute; the element being prepared measures
                    // from its own offset, so the two frames have to be lined up
                    // before either can be compared with the other.
                    const pendingOffset = pendingTierRef.current?.offset ?? 0
                    const absoluteNow = current
                      ? current.currentTime + offsetRef.current
                      : positionRef.current
                    const absoluteMark =
                      current && !current.paused ? absoluteNow + SWAP_LEAD_SECONDS : absoluteNow
                    const mark = absoluteMark - pendingOffset

                    // A stream that cannot be seeked is already parked: it was
                    // opened at its mark, so its zero *is* where it belongs.
                    // Trying to position it would either do nothing or, with a
                    // mark computed in the wrong frame, hand over immediately
                    // and jump the viewer forward.
                    if (pendingTierRef.current && !pendingTierRef.current.tier.seekable) {
                      handoverToBack()
                    } else if (mark <= 0) {
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

                  // resumeAtRef is absolute; the element is not. A muxed stream
                  // opened at ten minutes needs to be told zero, not ten.
                  const resumeAt = resumeAtRef.current - offsetRef.current
                  if (resumeAt > 0 && resumeAt < element.duration) {
                    element.currentTime = resumeAt
                  }
                  // The new source is loaded and positioned: resume tracking.
                  swappingRef.current = false

                  // Start playing on arrival, audibly if the browser allows it.
                  //
                  // It usually will not on a fresh page: audible autoplay needs
                  // a gesture in the document, and a reload creates a new one,
                  // so a page opened or refreshed has none to offer. Left at
                  // that, every reload lands on a paused first frame.
                  //
                  // So a refusal falls back to muted — which is always allowed
                  // — and the sound comes back by itself at the first click or
                  // key press anywhere on the page. Nothing has to be read or
                  // pressed to fix it, which is why there is no longer a badge
                  // asking for one.
                  element.play().catch(() => {
                    element.muted = true
                    setMuted(true)
                    mutedByPolicyRef.current = true
                    void element.play().catch(() => undefined)
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
                        // Absolute position in the video. A muxed stream opened
                        // at a mark believes it starts at zero, so everything
                        // outside the element works in offset + currentTime or
                        // a video seeked to ten minutes reports itself as just
                        // beginning.
                        const absolute = e.currentTarget.currentTime + offsetRef.current
                        setPosition(absolute)
                        positionRef.current = absolute
                        // Frozen across a source swap, so the browser's reset to
                        // 0 cannot erase where the viewer actually was.
                        if (!swappingRef.current) {
                          resumeAtRef.current = absolute
                        }
                      }
                    : undefined
                }
                onProgress={
                  isFront
                    ? (e) => {
                        const ranges = e.currentTarget.buffered
                        if (ranges.length > 0) {
                          setBuffered(ranges.end(ranges.length - 1) + offsetRef.current)
                        }
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

      {/* focus-within, not just the pointer: tabbing to a control has to bring
          it back, or the chrome becomes unreachable from the keyboard — and the
          keyboard is what the eventual television remote maps onto. */}
      <div
        className={clsx(
          'absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/80 to-transparent px-3 pt-8 pb-2',
          'transition-opacity duration-200 ease-out',
          'focus-within:pointer-events-auto focus-within:opacity-100',
          // Invisible does not mean absent: without this, a click where the bar
          // used to be still lands on whatever button is under the pointer, and
          // the viewer pauses or mutes something they cannot see. Falling
          // through to the picture instead makes that click do what a click on
          // a bare video should — and the container wakes the chrome anyway.
          controlsVisible ? 'opacity-100' : 'pointer-events-none opacity-0',
        )}
        onFocusCapture={wakeControls}
      >
        <SeekBar
          position={position}
          duration={duration}
          buffered={buffered}
          // Seeking works from the first second now: the opening source is a
          // progressive file the browser can range-request. Only the muxed
          // stream — the fallback for videos publishing no progressive format
          // at all — has no index and cannot be seeked.
          disabled={!playable || !tier?.seekable}
          // While dragging, only the number moves; the stream is asked for once
          // the handle is released. On a tier that cannot be seeked each of
          // these would otherwise kill an ffmpeg and start another, dozens of
          // times across one drag.
          onScrub={(next) => setPosition(next)}
          onSeek={seekTo}
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
            <CaptionMenu
              tracks={subtitles}
              active={captions}
              onSelect={setCaptions}
              onOpenChange={trackMenu}
            />
          )}

          {/* Only offered when there is more than one way to play the video.
              A quality menu over a single source would be a control that
              cannot do anything. */}
          {qualityOptions.length > 1 && (
            <QualityMenu
              choice={quality}
              options={qualityOptions}
              playingLabel={tierLabel}
              onOpenChange={trackMenu}
              onSelect={(next) => {
                // Choosing again is a fresh decision: a viewer who asks for
                // 1080p after auto gave up on it should get an attempt, not
                // the memory of the last failure.
                // Choosing again is a fresh decision, so the attempt count
                // starts over: someone asking for 1080p after auto gave up
                // should get a try, not the memory of the last failure.
                setRemuxAttempts(0)
                setQuality(next)
              }}
            />
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
/**
 * Auto, and whichever fixed choices the video can actually offer.
 *
 * Three entries at most, because there are only three distinct ways to play
 * anything here: the downloaded file, the same resolution muxed live, and the
 * low progressive rendition. Offering 480p and 720p as well would cost the same
 * ffmpeg as 1080p and give a worse picture for it — see the grilling notes in
 * CLAUDE.md §8b.
 */
function QualityMenu({
  choice,
  options,
  playingLabel,
  onSelect,
  onOpenChange,
}: {
  choice: QualityChoice
  options: { value: QualityChoice; label: string }[]
  /** What is on screen right now, shown beside Auto so it is never a mystery. */
  playingLabel: string
  onSelect: (next: QualityChoice) => void
  /** Lets the player keep its chrome up while this is open. */
  onOpenChange: (open: boolean) => void
}) {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    onOpenChange(open)
    // Closing on unmount too, or a menu left open while the video changes
    // would pin the controls on screen for good.
    return () => {
      if (open) onOpenChange(false)
    }
  }, [open, onOpenChange])

  return (
    <div className="relative">
      <button
        type="button"
        aria-label="Quality"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10"
      >
        <Settings size={22} />
      </button>

      {open && (
        <ul className="absolute right-0 bottom-11 min-w-44 overflow-hidden rounded-lg bg-surface py-1 text-sm shadow-lg">
          {options.map((option) => (
            <li key={option.value}>
              <button
                type="button"
                onClick={() => {
                  onSelect(option.value)
                  setOpen(false)
                }}
                className={
                  'flex w-full items-center justify-between gap-4 px-4 py-2 text-left transition-colors duration-150 ease-out hover:bg-surface-hover ' +
                  (choice === option.value ? 'font-medium' : '')
                }
              >
                <span>{option.label}</span>
                {option.value === 'auto' && choice === 'auto' && (
                  <span className="text-xs text-text-2">{playingLabel}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function CaptionMenu({
  tracks,
  active,
  onSelect,
  onOpenChange,
}: {
  tracks: SubtitleTrack[]
  active: string | null
  onSelect: (language: string | null) => void
  /** Lets the player keep its chrome up while this is open. */
  onOpenChange: (open: boolean) => void
}) {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    onOpenChange(open)
    return () => {
      if (open) onOpenChange(false)
    }
  }, [open, onOpenChange])
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
  onScrub,
  onSeek,
}: {
  position: number
  duration: number
  buffered: number
  disabled: boolean
  /** Called continuously while dragging. Moves the readout, nothing else. */
  onScrub: (next: number) => void
  /** Called once, when the handle is released or a key press lands. */
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
        // Dragging and committing are separate events, because on a muxed
        // stream committing means killing an ffmpeg and starting another. A
        // range input fires change continuously while dragging; the seek itself
        // waits for the pointer or key to be released.
        onChange={(e) => onScrub(Number(e.target.value))}
        onPointerUp={(e) => onSeek(Number(e.currentTarget.value))}
        onKeyUp={(e) => onSeek(Number(e.currentTarget.value))}
        onBlur={(e) => onSeek(Number(e.currentTarget.value))}
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
