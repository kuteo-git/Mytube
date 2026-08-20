import clsx from 'clsx'
import {
  Captions,
  CaptionsOff,
  Maximize,
  Pause,
  PictureInPicture2,
  Play,
  Settings,
  SkipForward,
  SlidersVertical,
  Volume1,
  Volume2,
  VolumeX,
} from 'lucide-react'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useQueryClient } from '@tanstack/react-query'
import type { MediaState, SubtitleTrack } from '@/features/catalog/domain/video'
import type { UnavailableReason } from '@/features/catalog/infrastructure/catalogRepository'
import type { StreamSources } from '@/features/catalog/infrastructure/catalogRepository'
import { resolveRemuxStart, useStream } from '@/features/catalog/application/queries'
import { MAX_CLIMB_REOPENS, remuxLead } from '@/features/watch/application/remux-lead'
import { seekElement } from '@/features/watch/application/player-seek'
import { useDownloadProgress } from '@/features/catalog/application/download'
import type { QualityChoice } from '@/features/watch/application/autoplay'
import {
  autoplayChainExhausted,
  resetAutoplayChain,
  useAutoplayPreference,
  useQualityPreference,
} from '@/features/watch/application/autoplay'
import {
  bindNarration,
  tickNarration,
  resetNarration,
  stopNarrationPlayback,
  loadViSubtitles,
  setNarrationVideo,
  setNarrationVoice,
  setNarrationGain,
  cancelTranslationPass,
  startTranslationPass,
  startNarrationPregen,
  cancelNarrationPregen,
  restartNarrationPregenForVoice,
  narrationProgress,
  pregenProgress,
} from '@/features/watch/application/narration'
import {
  attachElement,
  getAudioContext,
  isAttached,
  resumeAudio,
  setElementGain,
  applyEq,
  applyReverb,
} from '@/features/watch/application/audio-graph'
import {
  loadAudioSettings,
  saveAudioSettings,
  type AudioSettings,
} from '@/features/watch/application/audio-prefs'
import { EqualizerSetting } from '@/features/watch/ui/EqualizerPanel'
import { formatDuration as formatEta } from '@/features/watch/application/narration-eta'
import {
  loadNarrationPrefs,
  saveNarrationPrefs,
} from '@/features/watch/application/narration-prefs'
import { centreCues } from '@/features/watch/application/cue-placement'
import { useSwipeToMinimise } from '@/features/watch/application/use-swipe-to-minimise'
import { levelsFor } from '@/features/watch/application/narration-levels'
import { BAR_THUMB_WIDTH } from '@/features/watch/application/player-geometry'
import {
  deleteNarrationClips,
  deleteNarrationVtt,
  setCachePartition,
} from '@/features/watch/infrastructure/narration-cache'
import { useTranslateConfig } from '@/features/settings/application/queries'
import { loadNarrationAudioPrefs } from '@/features/settings/application/settings-prefs'
import {
  trackURL,
  useTranslatedTrack,
} from '@/features/watch/application/use-translated-track'
import {
  MACHINE_LANGUAGE,
  captionsSettled,
  desiredTrackMode,
  hasHumanVietnamese,
  subtitleOptions,
} from '@/features/watch/domain/subtitle-language'
import {
  canGoFullscreen,
  canUsePiP,
  enterPiP,
  goFullscreen,
  videoSupportsPiP,
} from '@/features/watch/application/player-presentation'
import { httpCatalogRepository as repo } from '@/features/catalog/infrastructure/catalogRepository'
import { formatDuration } from '@/shared/lib/format'
import { useCoarsePointer } from '@/shared/lib/pointer'
import { rememberLastWatched } from '@/features/watch/application/last-watched'

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
 * How much of the replacement must already have arrived beyond the point being
 * caught up to.
 *
 * A handover that lands exactly on the edge of what has been buffered stalls on
 * its first frame, which looks worse than the low rendition it replaced. A
 * second is enough to be sure there is something to play on arrival, and the
 * muxed stream fills far faster than it plays, so waiting for it costs nothing.
 */
const HANDOVER_CATCHUP_MARGIN_SECONDS = 1

/**
 * How long the controls stay up after the pointer stops moving.
 *
 * Three seconds is what youtube.com uses and what people therefore expect. Long
 * enough to travel from the picture to the seek bar without it vanishing on the
 * way, short enough that it does not sit over a film.
 */
const CONTROLS_IDLE_MS = 3000

/**
 * The same, for a finger.
 *
 * Longer, because a mouse keeps the chrome up simply by being over the picture
 * and a finger cannot. Every second of the three has to be spent reading and
 * reaching, with nothing holding the bar open in the meantime.
 */
const CONTROLS_IDLE_TOUCH_MS = 5000

/**
 * Where the viewer's mute preference is kept.
 *
 * Version two, because version one cannot be trusted. The volumechange handler
 * used to write to it, and that event fires for the player's own changes as
 * well as the viewer's — including the autoplay policy muting the video on
 * load. Anyone whose browser refused audible autoplay once had silence recorded
 * as a preference and handed back on every visit afterwards, and there is no
 * way to tell those entries from real ones. Starting a new key abandons them.
 */
const MUTED_KEY = 'yt-player-muted-v2'

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
 * How many times the climb to the local file may be lost before the player
 * settles for what it already has.
 *
 * A retry is nearly free here — the file is on disk, there is no process and no
 * request upstream — so the limit is not about cost. It is about the drive
 * disappearing (CLAUDE.md §8, risk 1), where every attempt fails identically
 * and forever, and a loop of a video that will not load is the worst possible
 * way to find that out.
 */
const MAX_LOCAL_ATTEMPTS = 3

/**
 * The height the full-quality tiers are labelled with when they do not say.
 *
 * The local file and the muxed stream are both produced at the configured
 * download height, and neither carries it in the stream answer.
 */
const remuxLabelHeight = 1080

/**
 * The rendition the muxed tier is served at unless asked otherwise.
 *
 * A copy of ingest's LIVE_HEIGHT, like the gateway's `remuxHeight` beside it,
 * and for the same reason: the alternative is asking the server what it is on
 * every stream request. What it is used for here is narrow — deciding whether a
 * request has to name a rendition at all — so a copy that drifted would cost a
 * redundant query parameter rather than a wrong picture.
 */
const DEFAULT_LIVE_HEIGHT = 720

/**
 * The renditions the muxed tier can be pinned to, highest first.
 *
 * Measured 2026-08-18 across sixteen videos of this library: the adaptive H.264
 * tracks at both heights, and the AAC audio beside them, answer 206 at the head
 * and the middle of the file alike. Only the progressive rendition the player
 * used to open on stopped serving.
 *
 * Auto never picks 1080p. This tier only has to last until the copy lands — a
 * median of thirteen seconds — and being ready before the viewer reaches its
 * mark is the whole difficulty with it, so twice the bytes is the wrong trade
 * to make on the viewer's behalf. Choosing it by hand is a different matter:
 * that is somebody who has decided to wait.
 */
const LIVE_HEIGHTS = [1080, 720] as const

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
function sourceURL(tier: Tier, offsetSeconds: number, audioStart = 0): string {
  if (tier.name !== 'remux') return tier.url
  const params = new URLSearchParams()
  // The rendition the mux is assembled at. Sent only when it differs from the
  // server's own LIVE_HEIGHT, so an ordinary stream asks for exactly the URL it
  // always did and a pinned one asks for what it was pinned to.
  //
  // Measured 2026-08-18 on the running stack: `?height=1080` returns H.264 1080p
  // with AAC beside it, 124 MB in thirty seconds. Auto stays at 720 because this
  // tier is only useful if it is ready before the viewer reaches its mark, and
  // half the bytes is half the wait — not because 1080p cannot be served.
  if (tier.height && tier.height !== DEFAULT_LIVE_HEIGHT) {
    params.set('height', String(tier.height))
  }
  if (offsetSeconds <= 0) {
    return params.size > 0 ? `${tier.url}?${params.toString()}` : tier.url
  }
  params.set('t', offsetSeconds.toFixed(3))
  // Where the audio should be seeked to, which is not where the video is.
  // ffmpeg's input seek lands on the nearest keyframe at or before the mark, so
  // the video starts earlier than asked while the audio starts almost exactly
  // on it — and the muxer then pulls both down to zero, turning that difference
  // into sound running ahead of picture. Measured at 2.008s. Sending the audio
  // to the video's keyframe is what removes it; see the server's OpenRemux.
  if (audioStart > 0) params.set('audioAt', audioStart.toFixed(3))
  // Cache-bust so the browser never serves a stale stream when seeking.
  params.set('_', String(Date.now()))
  return `${tier.url}?${params.toString()}`
}

/**
 * Every way this video can be played, best first.
 *
 * Order is not preference between equals. The local file is complete and
 * seekable; the muxed stream is the same resolution but has no index, so
 * seeking it means reopening it; the instant upstream seeks freely but is
 * whatever low rendition YouTube still publishes muxed.
 */
function availableTiers(sources: StreamSources | undefined, choice: QualityChoice): Tier[] {
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
      // Pinning the high rendition is an order, and before the copy lands the
      // muxed stream is the only thing that can carry it out. Auto keeps the
      // server's own height: it is choosing on the viewer's behalf, and this
      // tier is worth having only while it is ready sooner than the download.
      height: choice === 'high' ? LIVE_HEIGHTS[0] : sources.remux.height,
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

/**
 * How close to the exit a pause has to be to be the system's rather than the
 * viewer's.
 *
 * iOS lets go of the video in the same breath as leaving full screen; a viewer
 * who stopped it inside the system player did so some moments earlier. There is
 * nothing else to tell the two apart — both are an ordinary `pause` on the same
 * element.
 */
const SYSTEM_PAUSE_MS = 120

/**
 * How long after leaving full screen iOS may still be letting go.
 *
 * A ceiling, not the test. Measured on a real iPhone the stop landed **299ms**
 * after the exit — past the 250ms this first allowed, which is exactly why a
 * clock is the wrong thing to decide on. Widening it far enough to be safe
 * would also start swallowing a viewer's own pause.
 *
 * So what decides is whether anybody has touched anything since the exit, and
 * this only stops the window hanging open for ever if the stop never comes.
 */
const SYSTEM_LETGO_CEILING_MS = 1500

/**
 * Reports what Apple's player actually does on the way out, when asked.
 *
 * `sessionStorage.setItem('yt-fs-debug', '1')` and reload. Three device reports
 * in a row said the video comes back stopped, and all three are equally
 * consistent with the handler never running at all — so reading the code
 * settles nothing. This says whether the events arrive, what the element
 * reports at each, and whether `play()` was allowed: iOS refuses one made
 * outside a user gesture, and that refusal is currently swallowed.
 */
/**
 * Whether to report what the fullscreen path is doing.
 *
 * Turned on by putting `?fsdebug=1` on the address, and remembered from there —
 * because the device this has to be diagnosed on is a phone, and a phone has no
 * console to type a `sessionStorage` line into. That was the first attempt, and
 * it is why the first round of evidence came back empty.
 *
 * `?fsdebug=0` turns it off again.
 */
export function Player({
  videoId,
  hue,
  durationSeconds,
  initialPositionSeconds,
  mediaState,
  subtitles,
  thumbnailURL,
  nextVideoTitle,
  onPlayNext,
  title,
  channelTitle,
  variant = 'full',
  onSwipeDown,
  onSwipeProgress,
  morph = 0,
  onClose,
  onExpand,
  pauseToken = 0,
  autoplay = true,
}: {
  videoId: string
  hue: number
  durationSeconds: number
  initialPositionSeconds: number
  mediaState: MediaState
  subtitles: SubtitleTrack[]
  /**
   * Shown until the first frame decodes.
   *
   * Deliberately the stored URL rather than the upgraded guess used on cards: a
   * poster has no error event to fall back from, so a maxresdefault that turns
   * out not to exist would leave the player blank — which is the thing this is
   * here to prevent.
   */
  thumbnailURL?: string
  nextVideoTitle?: string
  onPlayNext?: () => void
  /** The video title, shown in the miniplayer overlay on hover. */
  title?: string
  /** Shown beneath the title in the mobile bar, where there is room for it. */
  channelTitle?: string
  /**
   * Which shape the player is in.
   *
   * `full` is the watch page. `mini` is the desktop corner player, which is
   * still a picture you watch. `bar` is the mobile one, which is a strip along
   * the bottom: the picture shrinks to a thumbnail and the row becomes mostly
   * text and two buttons, because at that size a picture with controls over it
   * is neither watchable nor tappable.
   */
  variant?: 'full' | 'mini' | 'bar'
  /** Put the player away and go back to browsing. Absent where the gesture
   *  makes no sense — a player already in the corner has nowhere to go. */
  onSwipeDown?: () => void
  /** Reports the drag so the host can follow the finger. */
  onSwipeProgress?: (pixels: number | null) => void
  /**
   * 0..1 through a drag towards the corner.
   *
   * Nonzero only mid-gesture, and it cross-fades the chrome: the full player's
   * controls go out as the bar's row of title and buttons comes in, so the
   * shape and what is drawn in it arrive at the corner together. Without it the
   * picture shrinks all the way down and the layout changes in one frame at the
   * end, which is the moment the whole movement was smoothing over.
   */
  morph?: number
  /** Closes the miniplayer and stops playback entirely. */
  onClose?: () => void
  /** Navigates back to the full Watch page. */
  onExpand?: () => void
  pauseToken?: number
  /**
   * Whether to start on arrival. False when the player is being put back after
   * a restart: something offered back should wait to be accepted.
   */
  autoplay?: boolean
}) {
  const mini = variant !== 'full'
  const bar = variant === 'bar'

  // Drag the picture down to put it away and go back to browsing.
  //
  // The charter records this gesture being removed in 2026-08-03 as useless,
  // and it was: the old one minimised *in place*, leaving the player in the
  // corner of the page it was already on. Pulling a player down is a request to
  // go and look at something else, so this one navigates — which on a phone is
  // also what turns the player into the bar, so there is no second state to
  // maintain.
  const surfaceRef = useRef<HTMLDivElement>(null)
  const swipe = useSwipeToMinimise({
    enabled: variant === 'full' && Boolean(onSwipeDown),
    height: () => surfaceRef.current?.getBoundingClientRect().height ?? 0,
    onDrag: onSwipeProgress ?? (() => {}),
    onCommit: () => onSwipeDown?.(),
  })
  const { data: sources, isPending: resolvingStream, isError: streamFailed } = useStream(videoId)
  // Playing from upstream always schedules a copy, so a job is coming even if
  // the queue has not caught up yet.
  const download = useDownloadProgress(videoId, Boolean(sources) && !sources?.local)
  const queryClient = useQueryClient()

  const [quality, setQuality] = useQualityPreference()
  // After the preference, which it now reads: pinning the high rendition
  // changes what the muxed tier asks the server for.
  const tiers = useMemo(() => availableTiers(sources, quality), [sources, quality])
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
  // Bumped to send the climb round again when nothing else would. Only the
  // attempt a seek is given for free needs it: every other abandonment already
  // moves remuxAttempts, and it is that movement the effect watches.
  const [climbAttempt, setClimbAttempt] = useState(0)
  const remuxFailed = remuxAttempts >= MAX_REMUX_ATTEMPTS
  // The same idea for the local file, kept apart from the muxed stream's count
  // because the two fail for unrelated reasons: the mux fails on a connection
  // that cannot carry it, the local file on a disk that is not there.
  const [localAttempts, setLocalAttempts] = useState(0)
  const localFailed = localAttempts >= MAX_LOCAL_ATTEMPTS
  // How long the last muxed stream took from being claimed to being ready, and
  // how many times the current video's climb has been reopened for landing late.
  //
  // Kept in refs rather than state: nothing renders from either, and the lead is
  // read inside the climb effect, which must not re-run because a measurement
  // arrived. Cleared per video with everything else — a measurement belongs to
  // one video's bitrate and length, not to the next one's.
  const remuxPrepMsRef = useRef<number | undefined>(undefined)
  const claimStartedAtRef = useRef(0)
  const climbReopensRef = useRef(0)
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
  // Same reason: a seek needs to know whether a low rendition exists to detour
  // through, and the list is rebuilt whenever the stream answer is re-polled.
  const tiersRef = useRef<Tier[]>(tiers)
  tiersRef.current = tiers
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
  /**
   * Hold the element, and put it into the audio graph the moment it exists.
   *
   * A ref callback rather than a mount effect, because the two layers are behind
   * `playable` and are not in the tree on the first render. An effect with an
   * empty dependency list therefore ran against two nulls, attached nothing, and
   * never ran again — the graph was built and the filters were set, with no
   * signal passing through them. The equaliser moved nothing at all, and since
   * an unattached element plays perfectly well on its own there was no symptom
   * beyond that.
   *
   * This cannot drift for the same reason: it fires off the node's own lifetime,
   * so a layer that is unmounted and built again is attached again. Both
   * callbacks are stable — an inline one would be a new function every render,
   * which React answers by calling it with null and then with the element, over
   * and over.
   */
  const holdVideo = (ref: React.RefObject<HTMLVideoElement | null>) =>
    (el: HTMLVideoElement | null) => {
      ref.current = el
      // Idempotent, and it must be: this runs on every remount of the layer.
      if (el) attachElement(el)
    }
  const setVideoA = useCallback(holdVideo(videoARef), [])
  const setVideoB = useCallback(holdVideo(videoBRef), [])

  const front = useCallback(
    () => (frontIsARef.current ? videoARef.current : videoBRef.current),
    [],
  )
  const back = useCallback(
    () => (frontIsARef.current ? videoBRef.current : videoARef.current),
    [],
  )

  // Stop on request from outside — closing the miniplayer while the watch page
  // is still open. Zero is the initial token and means nothing has asked yet,
  // so it must not pause a video that is only just starting.
  useEffect(() => {
    if (pauseToken === 0) return
    front()?.pause()
  }, [pauseToken, front])
  // URL loaded in each element. The back one is set only while an upgrade is
  // being prepared, and cleared afterwards so an abandoned stream is torn down
  // rather than left pulling bytes.
  const [srcA, setSrcA] = useState<string | undefined>(undefined)
  const [srcB, setSrcB] = useState<string | undefined>(undefined)
  const frontSrc = frontIsA ? srcA : srcB
  const backSrc = frontIsA ? srcB : srcA
  const setBackSrc = frontIsA ? setSrcB : setSrcA
  const [playing, setPlaying] = useState(false)
  const [volume, setVolume] = useState(() => {
    const v = window.localStorage.getItem('yt-player-volume')
    return v !== null ? parseFloat(v) : 1
  })
  const [muted, setMuted] = useState(() => window.localStorage.getItem(MUTED_KEY) === '1')
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
  const [captions, setCaptionsRaw] = useState<string | null>(
    () => window.localStorage.getItem('yt-player-captions') || null,
  )
  const setCaptions = useCallback((next: string | null) => {
    if (next) window.localStorage.setItem('yt-player-captions', next)
    else window.localStorage.removeItem('yt-player-captions')
    setCaptionsRaw(next)
  }, [])
  const captionsRef = useRef<string | null>(null)
  const [narrationPrefs, setNarrationPrefs] = useState(loadNarrationPrefs)
  const narrationSpeaks = narrationPrefs.speak
  // Reading aloud is the only thing narration still decides. Showing the
  // translated text is choosing its track in the subtitle list, like any other
  // language the video carries.
  const narrationOn = narrationSpeaks
  const narrationOnRef = useRef(false)
  // The AudioContext is no longer the player's to own — `audio-graph.ts` holds
  // the single one that both narration and the equaliser feed. What used to be
  // an `audioCtxRef` here became two features quietly assuming they were the
  // only ones with a context.
  // Keep refs synchronised so callbacks that are intentionally stable (empty
  // dependency arrays) never read a stale closure value — particularly
  // handoverToBack, which copies text track modes across the swap.
  useEffect(() => { captionsRef.current = captions }, [captions])
  useEffect(() => { narrationOnRef.current = narrationOn }, [narrationOn])
  // Read for the same reason as the two above: `desiredTrackMode` needs to know
  // whether this is the bar, and the handover callback is deliberately stable.
  const barRef = useRef(false)
  useEffect(() => { barRef.current = bar }, [bar])
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

  // What kind of pointer touched this last.
  //
  // Held per interaction rather than decided per device. A tablet with a
  // keyboard has both, and the right answer depends on which one is being used
  // right now — not on how wide the screen is, which is what a breakpoint would
  // have told us.
  const pointerKindRef = useRef<'mouse' | 'touch'>('mouse')

  const wakeControls = useCallback((kind: 'mouse' | 'touch' = pointerKindRef.current) => {
    setPointerActive(true)
    window.clearTimeout(hideTimerRef.current)
    // A finger gets longer, because there is no hovering to keep the chrome up
    // between one deliberate tap and the next.
    hideTimerRef.current = window.setTimeout(
      () => setPointerActive(false),
      kind === 'touch' ? CONTROLS_IDLE_TOUCH_MS : CONTROLS_IDLE_MS,
    )
  }, [])

  const hideControls = useCallback(() => {
    window.clearTimeout(hideTimerRef.current)
    setPointerActive(false)
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
  //
  // `startAt` is where the viewer should end up, absolute, when that is not
  // simply where the stream begins: a muxed stream opened at ten minutes really
  // begins at the keyframe before it, and a seek through the low rendition has
  // to place the element itself. Absent means "wherever it opened".
  //
  // `url` is what makes the whole record trustworthy. Several things write here
  // — the climb, the probe that refines where a muxed stream begins, and a seek
  // — and a claim that has been replaced since the element started loading is
  // not a claim about that element any more. Applying one anyway is how the
  // player ended up believing it was on the muxed stream while showing the low
  // rendition: the offset came from one source and the picture from another, so
  // every position read as the two added together, and nothing would climb
  // because the tier it thought it was on was the one it wanted.
  const pendingTierRef = useRef<
    { tier: Tier; offset: number; url: string; startAt?: number } | undefined
  >(undefined)
  // True while the climb back to full resolution belongs to a seek rather than
  // to the opening of the video. Two things read it: the lead is shorter, and a
  // failure does not count against the tier.
  //
  // Counting it would be wrong in kind. "Tried once and could not keep up" is a
  // statement about the connection; reopening at a new mark is not a new claim
  // about the connection, it is the same stream asked for from elsewhere. Left
  // shared, two seeks would pin a video to 360p for the rest of its length —
  // making the scrub bar a way to lose quality permanently.
  const postSeekRef = useRef(false)
  // Absolute position in the video, offset included. Read by the effect that
  // opens a muxed stream, which needs to know where the viewer is without
  // taking position as a dependency and restarting on every tick.
  const positionRef = useRef(0)
  const handoverFrameRef = useRef(0)
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

  // Why this video cannot be fetched, when it cannot.
  //
  // Two witnesses, and either is enough: the catalogue's state, which is what a
  // page opened later sees, and the stream answer, which is what the request
  // that just discovered it sees. The reason itself only ever comes from the
  // server — it is upstream's word, not a guess made here.
  const unavailableReason: UnavailableReason | null = sources?.unavailable
    ? sources.unavailable.reason
    : mediaState === 'UNAVAILABLE'
      ? 'unavailable'
      : null

  const playable = Boolean(frontSrc) && !loadFailed
  // Captions no longer wait for the media file: ingest publishes them ahead of
  // the transfer, precisely so they are usable during upstream playback.
  const captionsAvailable = subtitles.length > 0
  // Narration is available when there are Vietnamese subtitles. We don't know
  // until the <track> elements load, so we check via hasVietnameseSubs().
  // Narration is available when there are Vietnamese or English subtitles.
  // English cues are translated via NLLB-200 before TTS.
  // Our own translation does not count — see hasHumanVietnamese.
  const hasVi = hasHumanVietnamese(subtitles)
  const hasEn = subtitles.some((s) => /^en/.test(s.language))
  const narrationAvailable = hasVi || hasEn
  // Translation only happens when there is nothing Vietnamese to read already:
  // loadViSubtitles takes a Vietnamese track in preference to translating one.
  // Whether this video can be translated at all: there is English to work from
  // and no Vietnamese track to prefer over it. Decides whether the translation
  // group appears — never whether it runs, which is the switch's job.
  const canTranslate = hasEn && !hasVi
  // Every track's address, so an effect can depend on the list actually
  // changing rather than on its length.
  const subtitleKey = subtitles.map((t) => t.url).join('|')



  /**
   * Put both video layers into the audio graph, once, for the life of the page.
   *
   * Not when the equaliser is switched on: `createMediaElementSource` may be
   * called once per element and cannot be undone, so a lazy attachment would
   * mean two different signal paths — one for viewers who opened the equaliser
   * and one for everyone else — and a hole in the sound at the moment of
   * switching. One path, always, is the only version that can be tested.
   *
   * Both layers, not just the one in front. The player swaps them, and an
   * element that reached the front unattached would be inaudible.
   *
   * The context this creates is born suspended, which is safe: `audio-graph.ts`
   * arms the gesture that starts it, and the effects below take two further
   * chances at the same thing.
   */
  // Build the context early, before any element needs it. Attaching the layers
  // is `setVideoA`/`setVideoB` above — they arrive later than this, and later
  // than once.
  useEffect(() => {
    getAudioContext()
  }, [])

  /**
   * Two more chances to get the context running.
   *
   * A suspended context is now silence rather than a missing equaliser, so this
   * does not rely on the gesture listener alone. `play` covers the ordinary case
   * — whatever the viewer pressed was a gesture — and `visibilitychange` covers
   * iOS, which suspends the context when the app goes away and does not resume
   * it on the way back.
   */
  useEffect(() => {
    const onVisible = () => {
      if (document.visibilityState === 'visible') resumeAudio()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [])

  /**
   * How this device sounds: the equaliser curve and the room.
   *
   * Held here rather than in the panel so it survives the panel being closed,
   * and pushed into the graph by an effect so that the only way to change the
   * sound is to change this state — there is no second path that writes filters
   * directly.
   *
   * Two effects rather than one, keyed on the two halves. Rebuilding an impulse
   * response is a loop over hundreds of thousands of samples, and moving an
   * equaliser slider has nothing to say to it.
   */
  const [audio, setAudioState] = useState(loadAudioSettings)
  useEffect(() => { applyEq(audio.eq) }, [audio.eq])
  useEffect(() => { applyReverb(audio.reverb) }, [audio.reverb])
  const setAudio = useCallback((next: AudioSettings) => {
    saveAudioSettings(next)
    setAudioState(next)
  }, [])

  // Narration tick: runs every animation frame, reads VTT cues, pre-fetches
  // TTS audio and plays clips at their scheduled times through Web Audio API.
  // Use setInterval instead of requestAnimationFrame so the tick loop
  // keeps running when the tab is hidden (rAF is paused by the browser).
  // 100 ms is fast enough for TTS scheduling without wasting CPU.
  useEffect(() => {
    if (!narrationSpeaks) return

    // Pause and seek come from the element's own events, not from this timer.
    // A minute of narration is placed on the audio timeline in advance so it
    // survives the tab going to the background — and in the background the
    // timer is the first thing to stop, which is exactly when a pause pressed
    // on a lock screen has to be noticed.
    // Rebound whenever the front layer changes, not once at setup.
    //
    // The player keeps two <video> elements and swaps which is on screen — when
    // the downloaded file replaces the upstream one, or the quality changes.
    // Binding once left the listeners on whichever element happened to be in
    // front at the time, so pause and play on the element actually showing went
    // unheard: no rewind, and the cursor stayed out where the prefetch had left
    // it, which is minutes of silence after pressing play. Switching Read aloud
    // off and on appeared to fix it because that tears this effect down and
    // rebuilds it against the current element.
    let bound: HTMLVideoElement | null = null
    let unbind: (() => void) | undefined
    const bindTo = (el: HTMLVideoElement | null) => {
      if (el === bound) return
      unbind?.()
      bound = el
      unbind = el ? bindNarration(el) : undefined
    }
    bindTo(front())

    const id = setInterval(() => {
      const el = front()
      bindTo(el)
      const ctx = getAudioContext()
      if (!el || !ctx) return
      tickNarration(el, ctx)
    }, 100)

    return () => {
      clearInterval(id)
      unbind?.()
      // Stop the voice, keep the cues: this tears down whenever the output mode
      // stops including a voice, which is not the same as leaving the video.
      stopNarrationPlayback()
      // The context is deliberately left running. It used to be suspended here,
      // on the reasoning that nothing else was using it — which stopped being
      // true when the video started going through it. Suspending it now takes
      // the sound out of the player entirely, and switching Read aloud off is
      // not a request for silence.
    }
  }, [narrationSpeaks, front])

  // Restore stored volume/muted on the video element, and duck the video
  // audio when narration is active so the TTS voice is clearly audible.
  // Duck video audio while narration is active so the TTS voice is clear.
  // Only applies when the video actually has Vietnamese subtitles — otherwise
  // narrationOn could be stuck true from localStorage with no button to turn
  // it off, permanently halving the volume.
  // Narration audio settings live on this device and are edited on /settings.
  // Re-read on focus and on the storage event so a change made there — or in
  // another tab — reaches a player that is already on screen, which is what
  // makes dragging a slider audible against a running video.
  const [audioPrefs, setAudioPrefs] = useState(loadNarrationAudioPrefs)
  useEffect(() => {
    const reread = () => setAudioPrefs(loadNarrationAudioPrefs())
    window.addEventListener('focus', reread)
    window.addEventListener('storage', reread)
    window.addEventListener('yt-narration-audio-prefs', reread)
    return () => {
      window.removeEventListener('focus', reread)
      window.removeEventListener('storage', reread)
      window.removeEventListener('yt-narration-audio-prefs', reread)
    }
  }, [])

  // A change of voice invalidates every clip already made.
  //
  // The voice is part of each clip's key at both tiers, so nothing can be
  // misread as the new voice — but with a whole video prepared in advance, the
  // old recordings are a couple of hundred megabytes of a reading nobody will
  // hear again. So they go, and the sweep starts over.
  //
  // The first run is skipped on purpose: mounting the player is not a change of
  // voice, and clearing here would delete the clips of the video just opened
  // and re-synthesise the lot.
  const lastVoiceRef = useRef<string | null>(null)
  useEffect(() => {
    setNarrationVoice(audioPrefs.voice)
    const previous = lastVoiceRef.current
    lastVoiceRef.current = audioPrefs.voice
    if (previous === null || previous === audioPrefs.voice) return
    // Fired from the stored preference rather than from the picker, so this is
    // the settled choice — scrolling a list of voices writes nothing.
    void deleteNarrationClips(videoId)
    restartNarrationPregenForVoice()
  }, [audioPrefs.voice, videoId])

  const ducking = narrationSpeaks && narrationAvailable
  const levels = levelsFor({
    master: volume,
    muted,
    narrating: ducking,
    narrationLevel: audioPrefs.voiceLevel,
    duckLevel: audioPrefs.duckLevel,
  })

  useEffect(() => {
    setNarrationGain(levels.narration)
  }, [levels.narration])

  /**
   * Loudness, applied in the graph rather than on the element.
   *
   * `levelsFor` is unchanged and still decides the number — master volume, mute,
   * and the duck while the voice speaks. What moved is where the number lands.
   * Once an element is routed into Web Audio, whether its own `volume` still
   * attenuates the signal is not something to discover per browser on a
   * television, so a gain node does the arithmetic somewhere it plainly works.
   *
   * `el.muted` stays set as well. It costs nothing, it is the flag the mute
   * button reads back, and if a layer ever escapes the graph it is the one thing
   * that still holds.
   *
   * The back layer is pinned silent. It is loading — and, before a handover,
   * playing — out of sight, and both layers feed the same filters.
   */
  useEffect(() => {
    const el = front()
    const hidden = back()
    if (hidden) {
      if (isAttached(hidden)) setElementGain(hidden, 0)
      else hidden.volume = 0
    }
    if (!el) return
    el.muted = muted
    // The fallback is not decoration. A browser with no Web Audio — an older
    // television, which is where this is headed — attaches nothing, and then a
    // gain node nobody built would leave the volume slider inert and the video
    // permanently at full. Whichever path this element is actually on is the one
    // that gets the number.
    if (isAttached(el)) setElementGain(el, levels.video)
    else el.volume = levels.video
    // `playable` is in here because it is what puts the two layers into the tree.
    // A freshly attached element's gain starts at zero — so that a hidden layer
    // is never heard on the way in — and if this did not run again after that,
    // the fresh element would be the one in front and permanently silent.
  }, [levels.video, muted, front, back, frontSrc, frontIsA, playable])

  // Reset narration state when moving to a new video.
  useEffect(() => { resetNarration() }, [videoId])

  // Ending a pass belongs to leaving the video, and nothing else. It used to be
  // folded into resetNarration, which also runs on every swap between the two
  // <video> layers — so a pass was cancelled seconds after it began and the
  // status sat on "not started".
  //
  // The pre-generation sweep ends here too, and this is exactly the lifetime it
  // wants — which is worth spelling out, because the obvious reading is wrong.
  // The Player is mounted once, above the router (AppShell.tsx), so leaving the
  // watch page does not tear this down: the video folds into the corner and
  // keeps playing, and it still needs clips. What does tear it down is the video
  // changing, or the miniplayer being closed for good, which is what deactivate
  // does. Closing the tab needs nothing — the requests die with the page.
  useEffect(
    () => () => {
      cancelTranslationPass()
      cancelNarrationPregen()
    },
    [videoId],
  )

  // Prepare the whole video's narration ahead of playback.
  //
  // Gated on reading aloud alone, unlike the translation pass beneath it: a
  // viewer who merely selected the machine-translated subtitle track wants text,
  // and synthesising a thousand clips nobody will hear is the expensive half of
  // narration spent on nothing.
  //
  // It waits for the cue list itself rather than being re-run when one arrives,
  // because the cues are loaded by a different effect on a different schedule
  // and a dependency on them here would be a second thing to keep in step.
  useEffect(() => {
    if (!narrationOn || !narrationAvailable) return
    const el = front()
    startNarrationPregen(videoId, el ? el.currentTime : 0)
  }, [narrationOn, narrationAvailable, videoId, front])

  // Fetch and parse the best available VTT: Vietnamese first, then English
  // (which will be translated via NLLB-200).
  //
  // Asked for by the same two things that ask for a translation, and it has to
  // be both: these cues *are* what the pass translates, so gating them on
  // narration alone left the viewer who had only chosen the track waiting on a
  // list that was never going to be fetched. The pass started, found nothing to
  // do, and sat on "Not started" — which reads exactly like the switch having
  // done nothing at all.
  useEffect(() => {
    if (!narrationOn && captions !== MACHINE_LANGUAGE) return
    // Nothing has been published yet, so "no Vietnamese" is not an answer, it is
    // the absence of one. Choosing English from an empty list is how the
    // realtime engine started translating a video that had Vietnamese coming.
    if (!captionsSettled(subtitles)) return
    // Use the same regex as hasHumanVietnamese / hasEn so variants
    // yt-dlp produces (“en-orig”, “vi-orig”) are not missed. Missing
    // one left loadViSubtitles uncalled while startTranslationPass
    // waited forever in whenCuesReady — the progress line read
    // “Loading…” against subtitles already on screen.
    const viSub = subtitles.find(
      (s) => /^vi/.test(s.language) && s.language !== MACHINE_LANGUAGE,
    )
    if (viSub) { loadViSubtitles(viSub.url, 'vi'); return }
    const enSub = subtitles.find((s) => /^en/.test(s.language))
    if (enSub) loadViSubtitles(enSub.url, 'en')
  }, [narrationOn, captions, subtitles])

  // Tell narration which video it is for, so synthesised clips are filed beside
  // that video. Not folded into the translation pass: the realtime engine has
  // no pass, and its clips are worth keeping too.
  useEffect(() => {
    setNarrationVideo(videoId)
  }, [videoId])

  // Nobody is asking any more: stop the pass rather than let it carry on out of
  // sight. Translation is wanted while the track is selected or while there is
  // something to read aloud, and when neither is true it is work with no reader
  // — paid for in requests to a translator that is shared with everything else.
  useEffect(() => {
    if (!narrationOn && captions !== MACHINE_LANGUAGE) cancelTranslationPass()
  }, [narrationOn, captions])

  // Anchor the background translation pass wherever the viewer actually is.
  // Only the batch engine has a pass; NLLB translates as it speaks.
  useEffect(() => {
    // Two things ask for a translation, and either is enough.
    //
    // Reading aloud needs one, which is what this was written for. Choosing the
    // "Tiếng Việt (dịch máy)" track is a request in its own right, and it used
    // not to be treated as one: the track was offered in the menu and could only
    // be filled by switching on a different feature entirely, so it appeared to
    // exist and stayed nearly empty.
    //
    // There is no third switch. Both of these say what the viewer wants in
    // words about the thing itself; a separate "auto translate" only qualified
    // them, and being on by default it read as broken in both directions.
    if (!narrationOn && captions !== MACHINE_LANGUAGE) return
    // Same gate as the source-choice effect above, for the same reason and at
    // greater cost: a pass started against an empty caption list spends tokens
    // on a video whose own Vietnamese track is seconds away.
    if (!captionsSettled(subtitles) || hasVi) return
    const el = front()
    startTranslationPass(videoId, el ? el.currentTime : 0)
  }, [
    narrationOn,
    captions,
    videoId,
    subtitles,
    hasVi,
    front,
  ])

  // A human Vietnamese track arriving mid-pass ends the pass and takes its
  // output with it.
  //
  // The gates above stop this happening in the first place; this is for the
  // orders they cannot cover — captions republished later, or a pass already
  // running from before. Leaving the file behind would put two Vietnamese
  // entries in the caption menu permanently, which is what was reported.
  //
  // The stop is unconditional and local, so it costs nothing to be sure of. The
  // delete waits until the machine track is actually listed: most videos with
  // Vietnamese never had a translation written for them, and firing a DELETE at
  // every one of them is a request per video to remove a file that was never
  // there.
  useEffect(() => {
    if (!hasVi) return
    cancelTranslationPass()
  }, [hasVi, videoId])

  const droppedTranslationRef = useRef('')
  const hasMachineTrack = subtitles.some((s) => s.language === MACHINE_LANGUAGE)
  useEffect(() => {
    if (!hasVi || !hasMachineTrack) return
    if (droppedTranslationRef.current === videoId) return
    droppedTranslationRef.current = videoId
    void deleteNarrationVtt(videoId)
  }, [hasVi, hasMachineTrack, videoId])

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
    postSeekRef.current = false
    setTier(undefined)
    setOffsetSeconds(0)
    offsetRef.current = 0
    positionRef.current = initialPositionRef.current
    setRemuxAttempts(0)
    setLocalAttempts(0)
    // A preparation time belongs to one video's length and bitrate, and the
    // reopens belong to one video's climbs. Carried over, a short video would
    // set the lead for a long one and arrive at the same lateness the adaptive
    // lead exists to remove.
    remuxPrepMsRef.current = undefined
    claimStartedAtRef.current = 0
    climbReopensRef.current = 0
    setSeeking(false)
  }, [videoId])

  // What the viewer is actually watching, every time it changes.
  //
  // One effect on the state rather than a line at each `setTier`, because the
  // question this answers — "is this the live mux or the file on disk, and at
  // what height?" — is one somebody asks while testing, and a call site added
  // later would silently stop answering it. Pairs with the gateway's
  // `stream offered` and ingest's `live mux opened` on :8184: the three
  // together are one press of play, end to end.
  useEffect(() => {
    if (!tier) return
    console.info('[debug] tier', videoId, tier.name, `${tier.height ?? '?'}p`,
      'seekable', tier.seekable, 'quality', quality, 'offset', offsetSeconds, tier.url)
  }, [tier, videoId, quality, offsetSeconds])

  // The local file landing unlocks a player that had given up.
  //
  // `playable` is `frontSrc && !loadFailed`, and nothing but changing video
  // ever cleared `loadFailed` — so a stream that failed failed for the rest of
  // the page's life. That was survivable while there were two upstream tiers to
  // fall between; it is not now that the muxed stream is the only one before
  // the download lands, because the download is the one thing here that has
  // never failed. The viewer watched "The stream could not be loaded" sit over
  // a file that arrived seconds later, and reloading was the only way out —
  // which "worked" only by starting the machinery over.
  //
  // Keyed on the local URL rather than on the tier list: every other change to
  // that list is upstream shuffling sources that have just been measured not to
  // play, and clearing the failure for those would only loop.
  //
  // Only after giving up, and that guard is the whole of it: while a picture is
  // running, the file landing is an ordinary climb, and emptying the elements
  // then would black out a video that was playing perfectly well.
  useEffect(() => {
    if (!loadFailed || !sources?.local?.url) return
    retriedRef.current = false
    setLoadFailed(false)
    // Start over rather than climb. The climb prepares a replacement in the
    // hidden layer and hands over once it is ready, which is right when there
    // is a picture worth protecting — and there is not: the source in front is
    // the one upstream just refused. Cleared, the tier effect takes its other
    // branch and opens the file straight into the visible element.
    setSrcA(undefined)
    setSrcB(undefined)
    setTier(undefined)
    frontIsARef.current = true
    setFrontIsA(true)
    pendingTierRef.current = undefined
    upgradingToRef.current = undefined
    setRemuxAttempts(0)
    setLocalAttempts(0)
  }, [loadFailed, sources?.local?.url])

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

      // A video being resumed, on a stream that cannot be seeked, must be
      // *opened* where the viewer left off — there is no other way to get
      // there. Seeking it is what used to happen, and it does not fail: the
      // browser takes the number and then buffers toward it for as long as it
      // takes to stream there, showing nothing, reporting nothing.
      //
      // The same move the climb already makes, from the same two calls. The
      // stream is requested first and the keyframe asked for afterwards, so the
      // picture never waits on a second round trip: `sourceURL` opens at the
      // mark, and `resolveRemuxStart` only refines where the player believes
      // that stream begins.
      const resumeAt = initialPositionRef.current
      if (opening.seekable === false && resumeAt > 0) {
        const url = sourceURL(opening, resumeAt)
        setOffsetSeconds(resumeAt)
        offsetRef.current = resumeAt
        if (frontIsARef.current) setSrcA(url)
        else setSrcB(url)

        void resolveRemuxStart(videoId, resumeAt).then((actualStart) => {
          // The tier machinery may have moved on — another video, or the local
          // file landing. Only refine the stream this call was made for.
          if (actualStart <= 0 || offsetRef.current !== resumeAt) return
          setOffsetSeconds(actualStart)
          offsetRef.current = actualStart
        })
        return
      }

      setOffsetSeconds(0)
      if (frontIsARef.current) setSrcA(sourceURL(opening, 0))
      else setSrcB(sourceURL(opening, 0))
      return
    }

    // A seek already has the hidden element, and it is going somewhere this
    // effect does not know about. Starting a climb on top of it would replace
    // the replacement and leave the viewer where they were — the seek quietly
    // losing to the upgrade. The climb happens afterwards, from the new
    // position, when the tier this settles on re-runs the effect.
    if (pendingTierRef.current?.startAt !== undefined) return

    const wanted = targetTier(tiers, tier?.name, quality, remuxFailed)
    if (!wanted) return
    // The file is there and will not load. Stay on what is playing rather than
    // asking the same question of the same disk for the rest of the video.
    if (wanted.name === 'local' && localFailed) return

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

    if (wanted.name !== 'remux') {
      if (upgradingToRef.current === wanted.url) return
      upgradingToRef.current = wanted.url
      pendingTierRef.current = { tier: wanted, offset: 0, url: wanted.url }
      setBackSrc(wanted.url)
      return
    }

    // The lead is there because the playhead keeps moving while the mux is
    // being prepared: open it where the viewer is and they will have gone past
    // it by the time it arrives. A playhead that is not running arrives
    // nowhere, so the lead has nothing to buy and becomes a jump forward.
    //
    // The case that made this matter is the instant tier being refused — 403,
    // twice, in one of googlevideo's refusal waves. The front element then sits
    // at zero with an error on it and will never advance, the climb parks a
    // muxed stream at twenty seconds, and the viewer is handed a video that
    // begins twenty seconds in, clock included, for a video they never started.
    //
    // An error is the test rather than `paused`, which is also true of a front
    // still waiting for autoplay on a fresh page — that playhead is about to
    // move, and taking its lead away would spend the climb catching up.
    const stalled = front()?.error != null
    const lead = stalled ? 0 : remuxLead(remuxPrepMsRef.current, postSeekRef.current)
    const mark = positionRef.current + lead
    // Claim the attempt before asking anything, or the poll that re-runs this
    // effect a moment later would start a second mux for the same climb.
    //
    // The clock starts here rather than at `setBackSrc`, because everything from
    // here on is preparation the lead has to cover: the round trip that asks
    // where the mux will really begin, and then the mux itself.
    claimStartedAtRef.current = Date.now()
    const provisional = sourceURL(wanted, mark)
    upgradingToRef.current = provisional
    const claim = { tier: wanted, offset: mark, url: provisional }
    pendingTierRef.current = claim

    void resolveRemuxStart(videoId, mark).then((actualStart) => {
      // The tier machinery may have moved on while this was in flight — the
      // viewer seeking again, or the local file landing. Identity rather than a
      // flag scoped to this run of the effect: the effect re-runs on every poll
      // of the stream answer and returns early at the guard above, and a flag
      // cleared on the way out would cancel a climb that is still the current
      // one. What matters is whether this claim is still the one standing.
      if (pendingTierRef.current !== claim) return
      // The stream begins at the keyframe, not at the mark, and everything
      // outside the element counts from that. Zero means the server could not
      // say, in which case the mark is still the best guess available.
      const offset = actualStart > 0 ? actualStart : mark
      const url = sourceURL(wanted, mark, actualStart)
      upgradingToRef.current = url
      pendingTierRef.current = { tier: wanted, offset, url }
      setBackSrc(url)
    })
  }, [tiers, tier, quality, remuxFailed, localFailed, climbAttempt, frontSrc, setBackSrc, videoId, front])

  // Drop the climb in flight without holding it against the tier it was for.
  //
  // The front layer dying is not evidence about the layer being prepared: the
  // claim is discarded because the playhead it was measured from has stopped,
  // not because the mux failed, so counting it towards the three attempts that
  // switch 1080p off for the video would punish the wrong tier. Asking again
  // straight away rather than waiting for the next poll of the stream answer,
  // because the viewer is looking at a video that is not playing.
  const dropClimb = useCallback(() => {
    if (!pendingTierRef.current) return
    pendingTierRef.current = undefined
    upgradingToRef.current = undefined
    window.cancelAnimationFrame(handoverFrameRef.current)
    if (frontIsARef.current) setSrcB(undefined)
    else setSrcA(undefined)
    setClimbAttempt((n) => n + 1)
  }, [])

  // Ask for the same tier again from a fresh mark, because the mark was wrong.
  //
  // A muxed stream cannot be moved once opened — it has no index — so a stream
  // that turned out to be behind the viewer is not a stream that can be used,
  // however good it is. That says nothing about the tier, and counting it
  // against the three strikes was the trap: preparation takes about as long as
  // the lead allows, so on a long video every climb landed late, three late
  // climbs turned 1080p off for the rest of the video, and pinning it by hand
  // was the only thing that worked.
  //
  // The reopen is cheap in the sense that matters — nothing on screen changes,
  // the viewer stays on the rendition already playing — and it is not cheap in
  // ffmpeg, hence the bound. It should rarely be reached: by now the lead is
  // built from what the last attempt actually cost rather than from a guess.
  //
  // Returns false when the bound is spent, so the caller falls back to giving
  // up properly rather than silently doing nothing.
  const reopenClimb = useCallback(() => {
    if (climbReopensRef.current >= MAX_CLIMB_REOPENS) return false
    climbReopensRef.current += 1
    dropClimb()
    return true
  }, [dropClimb])

  // Give up on an upgrade and keep playing what already works. Failing to
  // prepare a better source is not a playback failure — nothing on screen
  // changes — so it must never surface as one.
  const abandonUpgrade = useCallback(() => {
    // A muxed stream that could not be prepared is not tried again in auto: the
    // connection that failed it will not have changed, and a second attempt is
    // more seconds of stalling to learn the same thing. A viewer who pinned
    // 1080p is not overruled — targetTier ignores this for them.
    //
    // Except after a seek, which is not evidence about the connection: the same
    // stream is being asked for from a different mark, and counting it would let
    // two turns of the scrub bar take 1080p away for the rest of the video.
    // The local file is the end of the climb, and losing it used to be final.
    //
    // `useStream` stops polling the moment the answer carries a local file, and
    // that poll was the only thing sending this effect round again — so a climb
    // to the local file that was abandoned for any reason was abandoned for
    // good, whatever the reason. The viewer stayed on 360p with the whole file
    // sitting on disk beside them, and the only ways out were pressing 1080p or
    // reloading, both of which work by starting the machinery over.
    //
    // Counted, because the drive going away (CLAUDE.md §8, risk 1) would
    // otherwise be an endless loop of a video that cannot load, on a television.
    if (pendingTierRef.current?.tier.name === 'local') {
      setLocalAttempts((n) => n + 1)
      setClimbAttempt((n) => n + 1)
    }
    if (pendingTierRef.current?.tier.name === 'remux') {
      if (postSeekRef.current) {
        // One free attempt per seek, not an exemption that lasts. The mark is
        // what was untried, and it has now been tried; leaving this set would
        // mean a single seek quietly switched the limit off for the rest of the
        // video, and a connection that cannot carry the mux would be asked
        // again and again for as long as the viewer stayed.
        postSeekRef.current = false
        // And try once more, now. Everything else that abandons a climb bumps
        // remuxAttempts, and it is that change which sends the effect round
        // again; without something of its own the free attempt would be the one
        // that quietly ended the climb, leaving the viewer on 360p until some
        // unrelated poll happened to wake the effect up.
        setClimbAttempt((n) => n + 1)
      } else {
        setRemuxAttempts((n) => n + 1)
      }
    }
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
    if (!current || !next) return
    // When the replacement was opened at a substantially different offset
    // than the current stream, this is a seek rather than an upgrade. The
    // playhead will never catch the new stream — it starts at a different
    // moment entirely — so commit as soon as there is data to show.
    const isSeek = pendingTierRef.current?.startAt !== undefined
    // upgradingToRef may have been cleared before onLoadedMetadata fires,
    // so for seeks we use pendingTierRef as the signal instead.
    if (!upgradingToRef.current && !isSeek) return

    // Refuse to apply a claim that is not about this element.
    //
    // Several things write the pending claim, and a seek and a climb can be in
    // flight within a second of each other. Whichever wrote last decided what
    // the player believed it was watching — regardless of what had actually
    // been loaded. Measured on a real seek: the picture was the 360p rendition
    // sitting at 2059.5s while the offset came from the muxed stream's keyframe
    // at 2056.8s, so every position read as the sum of the two, and the tier was
    // recorded as `remux` while `remux` was exactly what the player was still
    // trying to reach — which is why it then never climbed again.
    //
    // The element's own `src` is the only witness that cannot disagree with
    // itself, so it is what decides.
    const claimMatches = () => {
      const pending = pendingTierRef.current
      if (!pending) return false
      // Compared against the resolved absolute URL the element reports, which is
      // what `src` gives back once assigned.
      return next.src === new URL(pending.url, window.location.href).href
    }

    const commit = () => {
      // The claim moved on while this element was loading. Hand nothing over:
      // whatever replaced it is loading into this same element and will arrive
      // with a handover of its own, and swapping now would put the picture and
      // the offset permanently out of step.
      if (!claimMatches()) return

      // Carry across everything the viewer set, or the swap would silently undo
      // their mute and their subtitles.
      //
      // Volume is not among them any more: it belongs to the graph, not to the
      // element, and the effect that owns it re-runs on the swap and gives the
      // new front layer its gain. Copying `volume` here would only propagate the
      // constant 1 both elements now hold.
      next.muted = current.muted
      for (let i = 0; i < next.textTracks.length; i++) {
        const track = next.textTracks[i]
        track.mode = desiredTrackMode({
          trackLanguage: track.language,
          captions: captionsRef.current,
          bar: barRef.current,
          narrationOn: narrationOnRef.current,
        })
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
      // The seek is over once full resolution is back: a later failure is about
      // the connection again, and counts.
      if (pending?.tier.name === 'remux') postSeekRef.current = false
      pendingTierRef.current = undefined
      setSeeking(false)

      const wasPlaying = !current.paused
      justSwappedRef.current = true
      frontIsARef.current = !frontIsARef.current
      setFrontIsA(frontIsARef.current)
      upgradingToRef.current = undefined

      if (wasPlaying) void next.play().catch(() => undefined)
      current.pause()
      // Say it outright rather than waiting to be told. Both calls above are
      // about to fire events, in an order nothing here controls, and the answer
      // is already known at this point.
      setPlaying(!next.paused)

      // Carry the floating window across with everything else. The outgoing
      // element's source is dropped a few lines below, and dropping the source
      // of the element currently in picture-in-picture closes the window — so
      // the request has to be made before that, not after.
      if (document.pictureInPictureElement === current) {
        void next.requestPictureInPicture?.().catch(() => undefined)
      }
      // Dropping the old source releases it — for the muxed stream that is what
      // kills the ffmpeg process still muxing the rest of the video.
      if (frontIsARef.current) setSrcB(undefined)
      else setSrcA(undefined)
      swappingRef.current = false
    }

    /**
     * Decides whether the replacement can take over, and puts it where the
     * viewer is when it can.
     *
     * A stream opened at a mark believes that mark is its zero, and the mark is
     * rarely exactly where the viewer has got to: ahead of them while the climb
     * is being prepared, behind them if it ran late.
     *
     * Three answers, because there are three different situations and only two
     * of them used to be told apart:
     *
     *  - `'ready'`  — it is where it belongs, or has been moved there.
     *  - `'late'`   — it begins behind the viewer and cannot be moved, so this
     *                 mark is spent. Ask for another one.
     *  - `'empty'`  — it could be moved, but nothing has arrived at that point.
     */
    const catchUpToViewer = (): 'ready' | 'late' | 'empty' => {
      const within = current.currentTime + offsetRef.current - (pendingTierRef.current?.offset ?? 0)
      // Negative means the replacement begins *after* the viewer. Reading it as
      // "already in the right place" is what handed a viewer sitting at 0:00 a
      // stream that starts at 0:20, clock and all. There is nothing to wind back
      // to — a muxed stream calls its own mark zero — so the mark is wrong and
      // the answer is a new one.
      if (within < -0.05) return 'late'
      if (within <= 0.05) return 'ready'

      // **A stream that cannot be seeked must not be seeked, not even inside
      // what has arrived.** The muxed tier is reported `seekable: false` for a
      // real reason: fragmented MP4 down a pipe carries no index. The parking
      // step already respected that; this one did not, and it is the one that
      // runs when the climb is late — which it usually was, because preparation
      // takes about as long as the lead allows.
      //
      // This is a rule about correctness rather than a fix for a known fault:
      // the PIPELINE_ERROR_DECODE reports were *not* caused by it, since one of
      // them arrived with the viewer behind the mark, where no seek happens at
      // all. The matching numbers were arithmetic coincidence.
      //
      // Within the tolerance it is handed over where it stands, repeating under
      // a second of video. Beyond it the mark is spent, and a fresh one costs a
      // mux rather than the tier.
      if (pendingTierRef.current?.tier.seekable === false) {
        return within <= HANDOVER_OVERSHOOT_TOLERANCE ? 'ready' : 'late'
      }

      const ranges = next.buffered
      const filledTo = ranges.length > 0 ? ranges.end(ranges.length - 1) : 0
      // A margin, so the handover does not land on the very edge of what has
      // arrived and stall on its first frame.
      if (filledTo < within + HANDOVER_CATCHUP_MARGIN_SECONDS) return 'empty'
      // The guard above has already dealt with a stream that cannot seek, so
      // this asks a second time only to keep every playhead write on one road.
      // A refusal here means the element would not take the number; the layer
      // is not ready to be shown, which is what 'empty' says.
      if (seekElement(next, pendingTierRef.current?.tier, within) !== 'seeked') {
        return 'empty'
      }
      return 'ready'
    }

    /** Acts on a verdict that is not `'ready'`. */
    const giveUpOn = (verdict: 'late' | 'empty') => {
      // Late is about the mark, empty is about the stream, so only one of them
      // is evidence against the tier. A reopen that has run out of goes falls
      // through to abandoning, which is what counts a strike.
      if (verdict === 'late' && reopenClimb()) return
      abandonUpgrade()
    }

    // Paused: there is no mark to wait for, because the playhead is not going
    // to reach one. Exchange where the viewer is — which means putting the
    // replacement there first, or a stream opened at a keyframe behind them
    // would quietly wind the video back to it.
    if (current.paused) {
      const verdict = catchUpToViewer()
      if (verdict !== 'ready') {
        giveUpOn(verdict)
        return
      }
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
      if (isSeek) {
        // "Has data at the position it is sitting on" — which is the question,
        // and the one the old test got wrong. It asked whether half a second
        // had buffered from the start of the stream, but a seek leaves the
        // element somewhere that need not be near the start, and the browser
        // buffers around where it is rather than from zero. On a stream it
        // could not answer for, the condition simply never became true.
        if (next.readyState >= 2) {
          commit()
          return
        }
        handoverFrameRef.current = window.requestAnimationFrame(waitForMark)
        return
      }
      const backAbsolute = (pendingTierRef.current?.offset ?? 0) + next.currentTime
      const frontAbsolute = current.currentTime + offsetRef.current
      if (frontAbsolute >= backAbsolute - 0.05) {
        // Overshooting the mark means the replacement took longer to prepare
        // than it was given. Handing it over as it stands would rewind the
        // viewer by the difference, and a picture that jumps backwards is a
        // fault where a picture that stays low is only a disappointment.
        //
        // But the replacement can be caught up rather than thrown away. It was
        // opened before the playhead and has been filling ever since, so the
        // moment the viewer has reached is already inside it — and a move
        // within buffered data is one even an unindexed stream allows. This is
        // the same step a seek makes; it is only asked for a different reason.
        //
        // Abandoning was the old answer, and on long videos it was the wrong
        // one: preparation there takes about as long as the lead allows, so the
        // climb missed by a second or two, was thrown away, tried again from a
        // later mark, and missed again — a loop visible in the ingest log as a
        // mux opened and closed every dozen seconds. Three misses and auto gave
        // up on the tier for the rest of the video, which is why pinning 1080p
        // by hand was the only thing that worked.
        if (frontAbsolute > backAbsolute + HANDOVER_OVERSHOOT_TOLERANCE) {
          const verdict = catchUpToViewer()
          if (verdict !== 'ready') {
            giveUpOn(verdict)
            return
          }
          commit()
          return
        }
        commit()
        return
      }
      handoverFrameRef.current = window.requestAnimationFrame(waitForMark)
    }
    waitForMark()
  }, [front, back, captions, abandonUpgrade, reopenClimb])


  // A pending handover must not outlive the video it belongs to.
  useEffect(() => () => window.cancelAnimationFrame(handoverFrameRef.current), [])

  // Give up on a muxed stream that is taking too long to become playable.
  //
  // Without this the rail waits indefinitely on a connection that cannot
  // sustain the mux, and the viewer sits on the low rendition with no
  // indication that anything is being attempted or has failed.
  //
  // This used to exempt a pinned 1080p, on the grounds that a viewer who asked
  // for it should keep it. That reasoning left the one branch where waiting was
  // unbounded — and it is the branch a seek lands in, so the seek that could not
  // be served waited for ever with "Seeking…" on screen and nothing behind it.
  // Pinning still means the climb is attempted again; it cannot mean the player
  // is allowed to stop answering.
  useEffect(() => {
    if (!backSrc) return
    const timer = window.setTimeout(() => {
      if (pendingTierRef.current?.tier.name === 'remux') abandonUpgrade()
    }, REMUX_PATIENCE_MS)
    return () => window.clearTimeout(timer)
  }, [backSrc, abandonUpgrade])

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
   * What a press on the picture itself means.
   *
   * With a mouse it plays and pauses, as it does on every video on the web.
   *
   * With a finger it shows and hides the controls instead, which is what a
   * phone does. The two cannot both be the tap: a finger has no way to hover,
   * so the only way to bring the bar up is to touch the picture — and if that
   * also started or stopped the video, then looking at the controls would mean
   * interrupting what you were watching, every time, and dismissing them would
   * mean interrupting it again.
   */

  // Declared here rather than beside the other layout state: the settings below
  // size their touch targets from it, and they are built first.
  const coarse = useCoarsePointer()

  // Polled in one place so the subtitle option and the line under it cannot
  // disagree about what the translator is doing. Only while a translation is
  // wanted: this re-renders the player, and a video nobody is translating
  // should not pay for it twice a second.
  //
  // "Wanted" had to grow to include the track being selected. The pass ran
  // perfectly well without narration — but nothing read its progress, so the
  // state kept the value it was given at mount, and `idle` renders as "Not
  // started". A translation quietly working while the screen insists it has not
  // begun is worse than one that has not begun, because the viewer's next move
  // is to press something else.
  const [progress, setProgress] = useState(narrationProgress)
  // The voice half of the same pipeline. Polled on the same tick and from the
  // same place, because the two are read together — a viewer looking at this
  // panel wants to know when they will hear something, and translation finishing
  // is only half of that answer.
  const [speechProgress, setSpeechProgress] = useState(pregenProgress)
  useEffect(() => {
    if (!narrationOn && captions !== MACHINE_LANGUAGE) return
    const id = window.setInterval(() => {
      setProgress(narrationProgress())
      setSpeechProgress(pregenProgress())
    }, 500)
    return () => window.clearInterval(id)
  }, [narrationOn, captions])

  // Translations are filed under the model that produced them, so the player
  // has to know which model is configured before it reads or writes the cache.
  const { data: translateConfig } = useTranslateConfig()
  // Told even when the answer is "none configured". Announcing only a model
  // that exists left the pass waiting on a reply that was never going to come,
  // and a wait is indistinguishable from work: it reported "Loading subtitles…"
  // against a subtitle file that had loaded minutes earlier.
  useEffect(() => {
    if (translateConfig) setCachePartition(translateConfig.model ?? '')
  }, [translateConfig])

  // The translated track only exists once the file behind it has been written,
  // and the subtitle list was fetched long before that.
  const trackRevision = useTranslatedTrack(
    videoId,
    progress.vttVersion,
    progress.phase === 'done',
  )

  // <track> elements are created synchronously by React, but the browser
  // initialises the backing TextTrack objects asynchronously (microtask).
  // useLayoutEffect runs before that — textTracks.length is 0 on first fire.
  // Poll with rAF until the tracks are ready, then apply the stored preference.
  //
  // **Both layers, not just the one on screen.** The player keeps two `<video>`
  // elements and swaps which is in front; applying the preference to the front
  // one alone left the other holding whatever it was last told. Turn subtitles
  // off while B is in front and A keeps its track `showing` — then the next
  // tier climb brings A back and the subtitles return, switched off. Only a
  // video that is still downloading swaps layers, which is why it looked like a
  // fault of new videos.
  //
  // Setting both also means the incoming layer is already right at the moment
  // of a swap, so there is no frame where the subtitles are missing.
  useLayoutEffect(() => {
    const elements = [videoARef.current, videoBRef.current].filter(
      (el): el is HTMLVideoElement => el !== null,
    )
    if (elements.length === 0) return
    let frame = 0
    const apply = () => {
      for (const element of elements) {
        for (let i = 0; i < element.textTracks.length; i++) {
          const track = element.textTracks[i]
          const want = desiredTrackMode({
            trackLanguage: track.language,
            captions: captionsRef.current,
            bar: barRef.current,
            narrationOn: narrationOnRef.current,
          })
          // Written only when it differs. Assigning a mode is what fires the
          // list's `change` event, and the listener below answers that event by
          // assigning a mode.
          if (track.mode !== want) track.mode = want
          // A cue's own align/position beats any stylesheet, and YouTube's
          // auto-captions carry them on every line. The gateway strips them when
          // it serves a subtitle, but in the LAN deployment Caddy takes that route
          // over and would serve the file as it sits on disk — so the browser
          // fixes them too, and the two subtitle sources agree either way.
          centreCues(track.cues)
        }
      }
    }
    // Cues arrive after the track itself does, and a track only parses them
    // once its mode leaves 'disabled' — so the pass above sees an empty list
    // the first time and this is what catches the real one.
    const onCues = (e: Event) => {
      const track = (e.target as TextTrack | null) ?? null
      if (track) centreCues(track.cues)
    }

    // The preference is not only ours to write.
    //
    // A browser performs an automatic track selection of its own — the spec
    // lets it honour the viewer's system caption settings — and it does so when
    // tracks are attached to an element, which for a video being downloaded is
    // long after this effect last ran. The reported fault: open a new video,
    // wait for the file to land, and English subtitles appear while the setting
    // still reads Off.
    //
    // So the preference is enforced rather than applied. `change` fires on the
    // list whenever any track's mode is altered by anyone, and `addtrack` when
    // one appears; both answers are the same pass, and it writes only where the
    // mode disagrees, so our own writes settle instead of echoing.
    //
    // Guarded: a TextTrackList is an EventTarget in every browser, but jsdom
    // hands back a bare object, and a player that throws while mounting is a
    // worse fault than the one being fixed.
    const onListChange = () => apply()
    const listens = (target: { addEventListener?: unknown }) =>
      typeof target.addEventListener === 'function'

    // Listening waits for the tracks, exactly as applying does. Subscribing to
    // the lists this effect saw at mount would be subscribing to nothing: the
    // tracks it has to survive are the ones that do not exist yet.
    let lists: TextTrackList[] = []
    const attach = () => {
      lists = elements.map((el) => el.textTracks)
      for (const tracks of lists) {
        for (let i = 0; i < tracks.length; i++) {
          if (listens(tracks[i])) tracks[i].addEventListener('cuechange', onCues)
        }
        if (!listens(tracks)) continue
        tracks.addEventListener('change', onListChange)
        tracks.addEventListener('addtrack', onListChange)
      }
    }

    // The rAF loop waits for the browser to build the backing TextTrack
    // objects; only the wait is re-armed, so a `change` event arriving mid-wait
    // cannot start a second loop nobody cancels.
    const waitThenApply = () => {
      if (elements.every((el) => el.textTracks.length === 0)) {
        frame = requestAnimationFrame(waitThenApply)
        return
      }
      attach()
      apply()
    }
    frame = requestAnimationFrame(waitThenApply)

    return () => {
      cancelAnimationFrame(frame)
      for (const tracks of lists) {
        if (listens(tracks)) {
          tracks.removeEventListener('change', onListChange)
          tracks.removeEventListener('addtrack', onListChange)
        }
        for (let i = 0; i < tracks.length; i++) {
          if (listens(tracks[i])) tracks[i].removeEventListener('cuechange', onCues)
        }
      }
    }
    // Keyed on the tracks' addresses rather than on how many there are: the
    // translated track keeps its place in the list while its URL changes, so a
    // count would not notice it being replaced.
  }, [captions, frontSrc, frontIsA, subtitleKey, trackRevision, bar, narrationOn])


  const toggleSpeak = useCallback(() => {
    // Outside the updater, not inside it: React may run an updater more than
    // once, and building an AudioContext is not something to do twice.
    if (!narrationPrefs.speak) {
      // It has to be created and resumed inside the gesture, or the browser's
      // autoplay policy will not let it make a sound.
      getAudioContext()
      resumeAudio()
    }
    const next = { ...narrationPrefs, speak: !narrationPrefs.speak }
    saveNarrationPrefs(next)
    setNarrationPrefs(next)
  }, [narrationPrefs])


  /**
   * Translation, last and behind a rule.
   *
   * A report, not a setting: there is nothing to decide here. A translation is
   * asked for by choosing the track or by asking for the narration, and this
   * says how far that has got. It appears only while it is happening, because a
   * progress line for work nobody started is a status about nothing.
   */
  //
  // The two halves are reported separately because they are asked for
  // separately. Translation happens only where there is English and no
  // Vietnamese of the video's own; synthesis happens wherever there are
  // Vietnamese lines to say, translated or not. Gating both on translation hid
  // the voice's progress entirely on a video that came with Vietnamese — the
  // one case where nothing has to be translated and the wait is all synthesis.
  const showTranslateStatus =
    canTranslate && (narrationOn || captions === MACHINE_LANGUAGE)
  // Only shown to someone who asked to hear it: choosing the translated
  // subtitle track produces no speech, so a synthesis bar there would report on
  // work that is not happening.
  const showSpeechStatus = narrationOn && narrationAvailable
  const translateGroup =
    showTranslateStatus || showSpeechStatus ? (
      <>
        <li className="my-1 border-t border-line" aria-hidden />
        {/* Translating is what makes lines available to say, so the two read
            top to bottom in the order they occur. */}
        {showTranslateStatus && <NarrationStatus progress={progress} />}
        {showSpeechStatus && <SpeechStatus progress={speechProgress} />}
      </>
    ) : undefined

  const autoplayRow = onPlayNext ? (
    <SettingRow
      label="Autoplay"
      on={autoplayEnabled}
      onToggle={() => setAutoplayEnabled(!autoplayEnabled)}
    />
  ) : undefined

  // What this video can actually offer, built once: whether there is anything
  // is what decides if the setting appears at all. A row reading "Off" beside
  // nothing to turn on is a dead control — which is what a video with no
  // captions used to be given.
  const captionOptions = subtitleOptions(subtitles)

  const subtitleRows =
    captionOptions.length > 0 ? (
      <SegmentedSetting
        label="Subtitles"
        value={captions ?? 'off'}
        onSelect={(v: string) => setCaptions(v === 'off' ? null : v)}
        tall={coarse}
        options={[{ value: 'off', label: 'Off' }, ...captionOptions]}
      />
    ) : undefined

  /**
   * Whether the line on screen is also read aloud.
   *
   * A switch, not a fourth subtitle option: reading aloud is a different
   * question from what is written, and folding them together is what produced a
   * "Thuyết minh" group containing an option called "Phụ đề" while a separate
   * subtitle list sat a few rows above it.
   */
  const narrationRows = narrationAvailable ? (
    <>
      {/* Named for the one thing it does. "Read aloud" said nothing about which
          language came out, and this reads the Vietnamese translation and
          nothing else — so a viewer could reasonably have expected it to speak
          the English they were already watching. Switching it on also brings
          the translation into being, which is a great deal to hide behind two
          words that do not mention Vietnamese at all. */}
      <SettingRow
        label="Vietnamese narration"
        on={narrationSpeaks}
        onToggle={toggleSpeak}
      />
    </>
  ) : undefined

  const tapPicture = useCallback(() => {
    if (pointerKindRef.current !== 'touch') {
      toggle()
      return
    }
    if (pointerActive) hideControls()
    else wakeControls('touch')
  }, [toggle, pointerActive, hideControls, wakeControls])

  /**
   * Moves to an absolute position, however the current tier allows it.
   *
   * A seekable tier is a plain assignment. The muxed stream has no index, so it
   * cannot be repositioned — the picture has to be replaced. Which replacement
   * is the whole of the decision:
   *
   * **Through the low rendition, whenever there is one.** It is progressive and
   * seeks natively, so the viewer is at the new position in milliseconds, and
   * the climb back to full resolution then runs through the ordinary upgrade
   * path — the one that demonstrably works.
   *
   * Reopening the muxed stream directly is the fallback, and it is the fallback
   * because it was the bug: preparing a replacement at a wholly different mark
   * is not an upgrade, so the handover cannot wait for the playhead to catch up
   * and waits for the new stream to buffer instead. That wait had no end to it
   * on a pinned 1080p, which is what "Seeking…" and then nothing was. The two
   * ways of asking for the same picture had drifted apart; a seek now takes the
   * road that stayed working.
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

      // An ordinary seek, on a stream that has an index to seek in. The mark is
      // absolute and the element measures from its own offset, which is the one
      // conversion this function owns.
      //
      // Only a *non-seekable* refusal falls through to reopening the stream
      // below. An element that merely would not take the number is not a reason
      // to throw away the stream and build another one — it is a moment too
      // early, and the next attempt will land.
      if (seekElement(element, tierRef.current, target - offsetRef.current) !== 'refused-not-seekable') {
        return
      }

      const currentTier = tierRef.current
      if (!currentTier) return

      // The climb that follows belongs to this seek: shorter lead, and its
      // failure is not held against the tier.
      postSeekRef.current = true

      const instant = tiersRef.current.find((t) => t.name === 'instant')
      if (instant) {
        if (upgradingToRef.current === instant.url) return
        upgradingToRef.current = instant.url
        // startAt rather than offset: the low rendition begins at zero like any
        // ordinary file, and it is the element that has to be moved to the mark.
        pendingTierRef.current = { tier: instant, offset: 0, url: instant.url, startAt: target }
        setSeeking(true)
        if (frontIsARef.current) setSrcB(instant.url)
        else setSrcA(instant.url)
        return
      }

      // No progressive rendition published for this video: there is nothing to
      // detour through, so the stream is reopened at the mark as before.
      const url = sourceURL(currentTier, target)
      if (upgradingToRef.current === url) return
      upgradingToRef.current = url
      const claim = { tier: currentTier, offset: target, url, startAt: target }
      pendingTierRef.current = claim
      setSeeking(true)
      if (frontIsARef.current) setSrcB(url)
      else setSrcA(url)

      // Asked separately, and after the stream has been requested rather than
      // before it: this path has no faster tier to fall back on, so the picture
      // must not wait on a second round trip. The answer only refines where the
      // player believes the stream begins.
      void resolveRemuxStart(videoId, target).then((actualStart) => {
        if (actualStart <= 0 || pendingTierRef.current !== claim) return
        pendingTierRef.current = { tier: currentTier, offset: actualStart, url, startAt: target }
      })
    },
    [front, videoId],
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
    const element = front()
    setVolume(next)
    window.localStorage.setItem('yt-player-volume', String(next))
    window.localStorage.setItem(MUTED_KEY, next === 0 ? '1' : '0')
    if (element) {
      // The level itself reaches the graph through `levelsFor` and the effect
      // that follows it — `setVolume` above is the whole of it. Only mute is
      // still the element's business.
      element.muted = next === 0
      setMuted(next === 0)
    }
    // Touching the volume is a gesture, so audible playback is allowed again,
    // and it is the moment to take a suspended context up on it.
    resumeAudio()
  }

  const toggleMute = () => {
    const element = front()
    if (!element) return
    // A deliberate choice, so the automatic restore must not overrule it.
    element.muted = !element.muted
    setMuted(element.muted)
    window.localStorage.setItem(MUTED_KEY, element.muted ? '1' : '0')
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

      // Locally as well as on the server. The server's copy is history — what
      // was watched, on any device, ever. This one is narrower and answers a
      // different question: what this browser was in the middle of, so that
      // opening the app again can offer it back rather than starting blank.
      rememberLastWatched(videoId, element.currentTime, total)

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
    // Labelled with what pressing it delivers, not with what is on screen now.
    // The muxed tier is served at the server's height under auto and at the top
    // rendition once pinned, so reading the label off the current tier promised
    // 720p and produced 1080p.
    const high = tiers.find((t) => t.name === 'local' || t.name === 'remux')
    if (high) {
      const height = high.name === 'local' ? (high.height ?? remuxLabelHeight) : LIVE_HEIGHTS[0]
      options.push({ value: 'high', label: `${height}p` })
    }
    const low = tiers.find((t) => t.name === 'instant')
    if (low) options.push({ value: 'low', label: `${low.height ?? 360}p` })
    return options
  }, [tiers])

  /**
   * Resolution, as a segmented control like everything else in this panel.
   *
   * It was a list of rows because it predates the panel; every setting added
   * since is a segment, and one list among four segments read as an oversight
   * rather than a distinction.
   */
  const resolutionRow =
    qualityOptions.length > 1 ? (
      <SegmentedSetting
        label="Resolution"
        value={quality}
        tall={coarse}
        onSelect={(next: QualityChoice) => {
          // Choosing again is a fresh decision, so the attempt count starts
          // over: someone asking for 1080p after auto gave up should get a try,
          // not the memory of the last failure.
          setRemuxAttempts(0)
          setQuality(next)
        }}
        options={qualityOptions.map((o) => ({
          value: o.value,
          label: o.label,
          hint:
            o.value === 'auto' && quality === 'auto'
              ? `Currently ${tierLabel}`
              : undefined,
        }))}
      />
    ) : undefined


  // Tell the operating system what is playing.
  //
  // This is what puts the title, the channel and working transport buttons on a
  // phone's lock screen, and on Android it is the difference between playback
  // surviving a switch to another app and being killed as untracked noise. It
  // buys nothing on iOS, where the system suspends the page regardless — see
  // the note on the picture-in-picture button below.
  useEffect(() => {
    const session = navigator.mediaSession
    if (!session) return

    if (typeof MediaMetadata !== 'undefined') {
      session.metadata = new MediaMetadata({
        title: title ?? '',
        artist: channelTitle ?? '',
        artwork: thumbnailURL ? [{ src: thumbnailURL }] : [],
      })
    }

    const handlers: [MediaSessionAction, MediaSessionActionHandler | null][] = [
      ['play', () => void front()?.play().catch(() => undefined)],
      ['pause', () => front()?.pause()],
      ['nexttrack', onPlayNext ?? null],
      ['seekbackward', () => seekTo(positionRef.current - 10)],
      ['seekforward', () => seekTo(positionRef.current + 10)],
    ]
    for (const [action, handler] of handlers) {
      // Browsers reject actions they do not implement, one by one.
      try {
        session.setActionHandler(action, handler)
      } catch {
        /* not supported here */
      }
    }

    return () => {
      for (const [action] of handlers) {
        try {
          session.setActionHandler(action, null)
        } catch {
          /* not supported here */
        }
      }
    }
  }, [title, channelTitle, thumbnailURL, onPlayNext, seekTo, front])

  useEffect(() => {
    if (navigator.mediaSession) navigator.mediaSession.playbackState = playing ? 'playing' : 'paused'
  }, [playing])

  // Asked of the element rather than of the document, because iPhone answers no
  // to the document and yes to the video. A control that cannot do anything must
  // not be drawn at all (CLAUDE.md §5), and both of these were drawn and dead.
  // Finger-sized targets, and fewer of them. 36px is under the 44 Apple asks
  // for, which is most of why the bar felt cramped — not the count alone.
  const controlButton = clsx(
    'grid place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10',
    coarse ? 'h-11 w-11' : 'h-9 w-9',
  )

  // Whether the browser knows the idea at all — enough to decide on the first
  // render, before any element exists.
  const pipPossible = canUsePiP()
  const fullscreenAvailable = canGoFullscreen()

  // Whether this video in particular qualifies, which only the element can say.
  // Asked once it exists and kept, so the button appears or does not rather than
  // appearing and then refusing.
  const [pipAvailable, setPipAvailable] = useState(false)
  useEffect(() => {
    if (!pipPossible) return
    setPipAvailable(videoSupportsPiP(front()))
  }, [pipPossible, front, frontSrc])

  // Coming back from Apple's full-screen player.
  //
  // iOS hands the video to the system while it is full screen and hands it back
  // stopped. Leaving a video and returning to a still frame reads as the player
  // having given up.
  //
  // **The state that matters is the one on the way OUT, not the way in.** The
  // first version remembered `!paused` at `webkitbeginfullscreen` and gave up
  // if it was false — so enlarging a stopped video, pressing play inside the
  // system player, and swiping back out returned a still frame, because the
  // decision had been taken before any of that happened. What the viewer left
  // it doing is what it should go on doing, in both directions.
  //
  // Which the element itself reports at `webkitendfullscreen` — except that the
  // system's own pause arrives on **either** side of that announcement, and
  // CLAUDE.md §8b records both orderings. Arriving first, it makes a video the
  // viewer left running look stopped.
  //
  // So the two pauses are told apart by *when*, which is the only thing that
  // distinguishes them: the system lets go in the same breath as the exit,
  // while a viewer who stopped the video inside the system player did it some
  // moments earlier. A pause within SYSTEM_PAUSE_MS of the exit is the system's
  // and is disregarded; anything older is a decision and is honoured.
  //
  // A pause arriving *after* is the same event on the other side, so exactly
  // one is swallowed — see the `onPause` handler. One, because the system only
  // lets go once, and a second pause that close behind is somebody who really
  // did press stop.
  const resumeAfterFullscreenRef = useRef(false)
  const lastPauseAtRef = useRef(0)
  /** When full screen was last left. */
  const leftFullscreenAtRef = useRef(0)
  /**
   * When the viewer last touched anything.
   *
   * This is what tells the system's stop from the viewer's, and it is a better
   * question than "how long ago": iOS lets go without anybody doing anything,
   * while a pause the viewer meant is a pause they reached out and caused. A
   * clock can only guess at that, and guessed wrong by 49ms.
   */
  const lastInteractionAtRef = useRef(0)
  useEffect(() => {
    const seen = () => {
      lastInteractionAtRef.current = performance.now()
    }
    // Capture, so it is recorded before any handler can stop the event.
    document.addEventListener('pointerdown', seen, true)
    document.addEventListener('keydown', seen, true)
    return () => {
      document.removeEventListener('pointerdown', seen, true)
      document.removeEventListener('keydown', seen, true)
    }
  }, [])
  useEffect(() => {
    const elements = [videoARef.current, videoBRef.current].filter(
      (el): el is HTMLVideoElement => el !== null,
    )
    if (elements.length === 0) return

    // Cleared on the way in so a pause from just before the video was enlarged
    // cannot be mistaken for the system letting go on the way out.
    const onBegin = () => {
      lastPauseAtRef.current = 0
    }

    const onEnd = (event: Event) => {
      leftFullscreenAtRef.current = performance.now()
      const stopped = (event.target as HTMLVideoElement).paused
      const systemLetGo =
        stopped && performance.now() - lastPauseAtRef.current < SYSTEM_PAUSE_MS
      // Stopped, and stopped a while ago: the viewer meant it.
      if (stopped && !systemLetGo) return
      resumeAfterFullscreenRef.current = true

      // The layer on screen, not necessarily the one that was full screen: a
      // download finishing mid-flight swaps them, and playing the hidden one is
      // sound with no picture.
      const resume = () => {
        const el = front()
        if (el?.paused) void el.play().catch(() => undefined)
      }
      resume()

      // A ceiling, in case the stop never comes and the watch would otherwise
      // run for ever. Not the discriminator — the stop was measured arriving at
      // 299ms, and what decides is whether the viewer has touched anything.
      const timer = window.setTimeout(() => {
        resumeAfterFullscreenRef.current = false
      }, SYSTEM_LETGO_CEILING_MS)
      return () => window.clearTimeout(timer)
    }

    for (const element of elements) {
      element.addEventListener('webkitbeginfullscreen', onBegin)
      element.addEventListener('webkitendfullscreen', onEnd)
    }
    return () => {
      for (const element of elements) {
        element.removeEventListener('webkitbeginfullscreen', onBegin)
        element.removeEventListener('webkitendfullscreen', onEnd)
      }
    }
  }, [playable, front])

  // A copy is coming either way, but only one of these two is moving bytes:
  // there is a single worker, so a job still QUEUED is behind another video and
  // has transferred nothing. Drawing it as a progress bar at 0% claims a file
  // is filling when nothing has started — the same thing that made two videos
  // look like they were downloading at once on the activity page.
  const transferring = download?.state === 'RUNNING'
  const queuedBehind = download?.state === 'QUEUED'
  const downloadPercent = Math.round((download?.progress ?? 0) * 100)

  return (
    <div
      ref={surfaceRef}
      className={clsx(
        'group/player relative h-full w-full overflow-hidden bg-black',
        // The cursor goes with the chrome. Leaving an arrow sitting on a film is
        // the same distraction in miniature. Not in mini mode: the miniplayer
        // overlay needs the pointer to stay visible.
        !mini && !controlsVisible && 'cursor-none',
      )}
      style={{
        ...(playable
          ? undefined
          : { background: `radial-gradient(120% 90% at 50% 30%, hsl(${hue} 40% 22%), #000 70%)` }),
      }}
      onPointerMove={(e) => {
        // A finger sliding across the picture is not a reason to keep the
        // chrome awake; its own tap already did that.
        if (e.pointerType !== 'touch') {
          pointerKindRef.current = 'mouse'
          wakeControls('mouse')
        }
        swipe.move(e)
      }}
      onPointerDown={(e) => {
        pointerKindRef.current = e.pointerType === 'touch' ? 'touch' : 'mouse'
        wakeControls(pointerKindRef.current)
        swipe.down(e)
      }}
      onPointerUp={swipe.up}
      // A pointer the browser takes away mid-drag — a system gesture, a call
      // arriving — must not leave the player parked half-way to the corner with
      // nothing coming to finish the movement.
      onPointerCancel={swipe.cancel}
      // A mouse leaving takes the chrome with it: it is demonstrably elsewhere,
      // so there is nothing to wait for.
      //
      // A finger does not leave, it lifts — and the browser reports that as
      // `pointerleave` too, because the pointer has ceased to exist rather than
      // moved away. Acting on it meant the controls vanished the instant you
      // stopped touching them, which is also why nothing on the bar could be
      // pressed: the sequence is pointerdown, pointerup, pointerleave, click,
      // so the bar was made unclickable one event before the click arrived.
      onPointerLeave={(e) => {
        if (e.pointerType === 'touch') return
        hideControls()
        setOpenMenus(0) // dismiss menus so controls can hide
      }}
    >
      {/* Not in the bar, and fading out on the way into it.

          At 128px wide the picture is a thumbnail: a badge on it covers a
          quarter of what there is to see and is too small to read anyway. The
          same is true of the subtitles, which are switched off with it — see
          the caption effect above. */}
      {tier && tier.name !== 'local' && !bar && (
        <div
          style={morph > 0 ? { opacity: 1 - morph } : undefined}
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
          {queuedBehind && (
            <>
              <span className="h-3 w-px bg-white/25" />
              <span>Copy queued</span>
            </>
          )}
          {transferring && (
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
                ref={isA ? setVideoA : setVideoB}
                src={src}
                className={clsx(
                  'absolute top-0 left-0 h-full cursor-pointer',
                  // In the bar the picture is a thumbnail on the left rather than
                  // the whole surface: stretched across a 72px-tall strip it would
                  // be a smear, and the row is mostly text at that size anyway.
                  //
                  // `object-cover` from the first moment of a drag, not at the
                  // end of it. On a 16:9 host holding 16:9 video it changes
                  // nothing at full width, so adopting it early costs nothing
                  // and avoids the fit changing under the viewer at the exact
                  // moment the shape stops moving.
                  bar || morph > 0 ? 'object-cover' : 'w-full',
                )}
                // The hidden layer must stay laid out and decoding — display:none
                // would stop it buffering, which is the entire point of it.
                //
                // The width travels with the drag rather than switching at the
                // end of it. The host was already shrinking towards the corner
                // while the picture inside it stayed full-bleed until the last
                // frame, so the layout the movement was building arrived all at
                // once — exactly the jump the gesture exists to smooth over.
                // `calc` between a percentage and a length is what lets one
                // number carry it from "the whole surface" to "the thumbnail".
                style={{
                  opacity: isFront ? 1 : 0,
                  pointerEvents: isFront ? undefined : 'none',
                  width: bar
                    ? BAR_THUMB_WIDTH
                    : morph > 0
                      ? `calc(${(1 - morph) * 100}% + ${morph * BAR_THUMB_WIDTH}px)`
                      : undefined,
                }}
                aria-hidden={!isFront}
                playsInline
                // Only the visible layer: a poster on the hidden one would be
                // decoded for nothing, and would flash if the layers swapped
                // before it had a frame.
                poster={isFront ? thumbnailURL : undefined}
                // The layer being prepared has to buffer ahead of being needed;
                // metadata alone would leave it unable to take over.
                preload={isFront ? 'metadata' : 'auto'}
                // Clicking the picture toggles playback, the way every video
                // player on the web behaves.
                onClick={isFront ? tapPicture : undefined}
                // Attached to both layers, and asking which one is at the front
                // when the event *fires* rather than when this rendered.
                //
                // Binding by render-time front-ness was wrong in exactly the
                // case that matters: the handover calls play() on the element
                // that is about to come forward, while React still has it as the
                // back one, so its handler was undefined when the play event
                // arrived and the state never learned that playback had resumed.
                // The pause() on the outgoing layer did land, so the player sat
                // there playing while insisting it was paused — showing a play
                // button, and holding the controls open, since they are pinned
                // whenever nothing is playing.
                onPlay={() => {
                  // The element's sound reaches the speakers through the audio
                  // graph, so a context that is not running is a video playing
                  // in silence. Whatever started this was almost certainly a
                  // gesture; take it.
                  resumeAudio()
                  if (isA === frontIsARef.current) setPlaying(true)
                }}
                onPause={() => {
                  lastPauseAtRef.current = performance.now()
                  // A pause arriving in the moments after full screen ended is
                  // the system letting go, not the viewer stopping the video.
                  //
                  // Exactly one is swallowed, and the flag is cleared before
                  // anything else: the system lets go once, so a second pause
                  // this close behind is somebody who really did press stop, and
                  // swallowing that would make the button look broken.
                  // The system letting go, or the viewer stopping the video?
                  // Nobody touched anything since full screen ended, so this
                  // was not asked for.
                  const untouched =
                    lastInteractionAtRef.current <= leftFullscreenAtRef.current
                  if (resumeAfterFullscreenRef.current && untouched) {
                    resumeAfterFullscreenRef.current = false
                    const el = front()
                    if (el?.paused) void el.play().catch(() => undefined)
                    return
                  }
                  if (isA === frontIsARef.current) setPlaying(false)
                }}
                onVolumeChange={
                  isFront
                    ? (e) => {
                        // Follow the element's mute, and nothing else.
                        //
                        // This event fires for our own changes as much as for
                        // the viewer's, and preferences are written where the
                        // viewer expresses them: the mute button and the volume
                        // slider. Writing them from here once recorded the
                        // autoplay policy's own mute as a preference and handed
                        // silence back on every later visit.
                        //
                        // Volume is no longer read from the element at all.
                        // Loudness lives in the audio graph now, so the
                        // element's `volume` sits at 1 for the life of the page
                        // — and following it would have set the player to full
                        // volume the first time anything fired this event.
                        setMuted(e.currentTarget.muted)
                      }
                    : undefined
                }
                onLoadedMetadata={(e) => {
                  const element = e.currentTarget
                  if (!isFront) {
                    // A replacement being prepared for a seek is not parked
                    // ahead of the playhead — it is going somewhere else
                    // entirely, and the playhead will never arrive there. It is
                    // placed at the mark and handed over as soon as it can show
                    // anything.
                    // Only the claim that is about *this* source may position
                    // it. One written for a different one would place the
                    // element by arithmetic belonging to another stream.
                    const pending = pendingTierRef.current
                    const mine =
                      pending && element.src === new URL(pending.url, window.location.href).href
                        ? pending
                        : undefined

                    // What the preparation actually cost, for the next lead to
                    // be built from rather than guessed at. Recorded here
                    // because this is the moment the stream became usable, and
                    // only for the claim this element belongs to — a stale
                    // element reporting itself ready would otherwise time a
                    // climb it was not part of.
                    if (mine && mine.tier.name === 'remux' && claimStartedAtRef.current > 0) {
                      remuxPrepMsRef.current = Date.now() - claimStartedAtRef.current
                    }

                    if (mine?.startAt !== undefined) {
                      const within = mine.startAt - mine.offset
                      // A stream reopened at the mark begins at the keyframe
                      // before it, so there is a second or two to skip over.
                      //
                      // This used to skip it on any stream, on the reasoning
                      // that a move *inside what has already arrived* is one
                      // even an unindexed stream allows. CLAUDE.md §4 records
                      // the measurement that says otherwise: the browser failed
                      // on the audio packet at exactly the seek target —
                      // 0.766259 on a stream whose offset was 18.936 — and
                      // reported it as PIPELINE_ERROR_DECODE, on four videos at
                      // four different marks. Buffered is not the same as
                      // seekable.
                      //
                      // So it is asked rather than assumed, and a refusal is
                      // the ordinary outcome rather than an error: the viewer
                      // starts those couple of seconds earlier, and the bar
                      // still says where they truly are.
                      if (within > 0.05) seekElement(element, mine.tier, within)
                      handoverToBack()
                      return
                    }

                    // Park the replacement a moment ahead of the playhead and
                    // wait there. Seeking is what makes the exchange seamless,
                    // so a source that cannot seek hands over immediately.
                    const current = front()
                    // The mark is absolute; the element being prepared measures
                    // from its own offset, so the two frames have to be lined up
                    // before either can be compared with the other.
                    const pendingOffset = mine?.offset ?? 0
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
                    if (mine && !mine.tier.seekable) {
                      handoverToBack()
                    } else if (mark < -0.05) {
                      // The replacement starts after the viewer. It cannot be
                      // moved back to them — its mark is its zero — so handing
                      // it over would take them forward to it. This read as
                      // "both elements already agree on where they are", which
                      // is true only when the difference is nothing, not when
                      // it is negative.
                      abandonUpgrade()
                    } else if (mark <= 0) {
                      // Nothing has played yet, so both elements already agree
                      // on where they are.
                      handoverToBack()
                    } else if (Number.isFinite(element.duration) && mark < element.duration) {
                      // The branch above has already sent a non-seekable stream
                      // to the handover, so this only ever asks a stream that
                      // can answer. Through the same door regardless.
                      seekElement(element, mine?.tier, mark)
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

                  // Put the viewer back where they left off.
                  //
                  // resumeAtRef is absolute; the element is not. A muxed stream
                  // opened at ten minutes needs to be told zero, not ten.
                  //
                  // This was the fourth place to move a playhead and the only
                  // one that never asked whether it could. It did not matter
                  // while the opening tier was the progressive one, which seeks
                  // like any file — and became the whole of "the video will not
                  // start" the day the muxed stream became what every video
                  // opens on. Asked to seek a stream with no index the browser
                  // says nothing at all: it accepts the number and buffers
                  // toward it, which for an unindexed stream means streaming
                  // the whole way. Measured on `ZIaOBAjvc38`, left at 336s: the
                  // mux opened, delivered 3.6 MB, and the picture never
                  // appeared until the download landed 45 seconds later.
                  //
                  // The tier machinery opens such a stream *at* the mark
                  // instead — `sourceURL(tier, mark, audioStart)`, so its zero
                  // is already where the viewer wants to be, and `offsetSeconds`
                  // carries the difference. By the time the element reports its
                  // metadata there is nothing left to move, which is why a
                  // refusal here is silent rather than a fault.
                  const resumeAt = resumeAtRef.current - offsetRef.current
                  if (resumeAt > 0 && resumeAt < element.duration) {
                    seekElement(element, tierRef.current, resumeAt)
                  }
                  // The new source is loaded and positioned: resume tracking.
                  swappingRef.current = false

                  // Start playing on arrival, audibly, or not at all.
                  //
                  // Audible autoplay needs a gesture the document has not been
                  // given, so on a fresh page this is often refused. A refusal
                  // used to fall back to muted, which CLAUDE.md §8b had already
                  // decided against and which iPhone turns from a compromise
                  // into the normal case: Safari refuses far more readily, so
                  // the fallback was not a fallback there, it was the outcome.
                  // Silent playback then had to be undone by hand, and undoing
                  // it is not simply a matter of clearing `muted` — Safari
                  // wants the element played again inside a gesture, which
                  // nothing was doing.
                  //
                  // A first frame sitting still is honest about needing a press.
                  // A picture that moves without sound is not.
                  //
                  // And a video restored from a previous visit does not start at
                  // all: it is an offer, and an offer that begins playing on its
                  // own is not an offer.
                  if (autoplay) {
                    element
                      .play()
                      .catch((err) =>
                        console.error('[debug] autoplay play() rejected', videoId, element.src, err?.name, err?.message),
                      )
                  }
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
                onError={(e) => {
                  console.error(
                    '[debug] video element error',
                    videoId,
                    isFront ? 'front' : 'back',
                    e.currentTarget.error?.code,
                    e.currentTarget.error?.message,
                    e.currentTarget.src,
                  )
                  if (!isFront) {
                    // Only the stream that failed may be abandoned.
                    //
                    // The claim moves on without waiting for the element: the
                    // local file landing writes a new one and the `src` follows
                    // on the next commit, so a refusal still travelling from the
                    // stream that was there a moment ago arrives against a claim
                    // it has nothing to do with. Measured: the gateway answered
                    // the mux with a 502, the error landed after the download
                    // had, and it took the climb to the local file with it —
                    // after which nothing polls, nothing re-runs, and the viewer
                    // watches the rest at 360p. The same identity test the
                    // handover uses, for the same reason.
                    const claim = pendingTierRef.current
                    if (claim && e.currentTarget.src !== new URL(claim.url, window.location.href).href) {
                      return
                    }
                    // An upgrade that will not load is not a failure worth
                    // showing: what is on screen still works. Abandon it.
                    abandonUpgrade()
                    return
                  }
                  // Whatever climb is in flight was measured from this
                  // playhead, and this playhead has just stopped for good: it
                  // is parked a lead ahead of a viewer who will never travel
                  // that far. Letting it stand is what turned a refused instant
                  // tier into a silent jump to 1080p twenty seconds in. Dropped
                  // here so the next attempt is made from where the viewer
                  // actually is.
                  dropClimb()

                  // The tier in front of the viewer has failed, and that is a
                  // statement about this source, not about the video. A muxed
                  // stream can lose one of its two inputs part way through —
                  // the picture keeps arriving, the sound stops, and the
                  // browser rejects an audio packet — after which the element
                  // is dead and nothing was moving the player off it.
                  //
                  // `targetTier` has always known how to retreat: once
                  // `remuxFailed`, auto asks for the low rendition again. Only
                  // an abandoned *climb* ever counted a remux failure though,
                  // so a mux that broke after being handed over counted
                  // nothing, and the player sat on a dead element with a
                  // working 360p source one step away. The viewer's only way
                  // out was reloading, which works by starting over.
                  // Only where there is something to retreat *to*. Withholding
                  // an unverified instant URL is ordinary now, so the muxed
                  // stream is often the opening tier rather than a climb —
                  // visible in the ingest log as `live mux opened ... from=0`.
                  // Retreating from the only tier there is leads nowhere, and
                  // returning here swallowed the retry and the failure report
                  // with it: the video never started and never said why, which
                  // from the sofa is a next button that does nothing.
                  const retreatTo = tiers.find((t) => t.name !== 'remux')
                  if (tierRef.current?.name === 'remux' && retreatTo) {
                    setRemuxAttempts(MAX_REMUX_ATTEMPTS)
                    setClimbAttempt((n) => n + 1)
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
                      // The generated track carries a revision, because the file
                      // behind it is rewritten in place and a browser will not
                      // fetch an address it already has.
                      src={trackURL(track.url, track.generated, trackRevision)}
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
              ? // A failed stream is not a failed video. Before the copy lands
                // the muxed stream is the only source, and upstream refuses one
                // often enough that a dead end here would be the ordinary
                // outcome — while the download beside it carries on and
                // finishes, usually within seconds. So this says what is
                // actually happening, and the player starts on its own the
                // moment the file is there (see the effect on the local URL).
                transferring
                ? `Live streaming failed. Downloading instead — ${downloadPercent}%, it will start by itself.`
                : queuedBehind
                  ? 'Live streaming failed. The download is queued behind another video, and this will start by itself once it finishes.'
                  : 'The stream could not be loaded.'
              : unavailableReason
                ? unavailableCopy(unavailableReason)
                : mediaState === 'EVICTED'
                ? 'The media file was removed to reclaim disk space, and upstream has nothing directly playable. Re-download it to watch again.'
                : sources?.streamError
                  ? sources.streamError
                  : streamFailed
                    ? 'Nothing playable is available yet. The download has to finish first.'
                    : 'No media file available yet.'}
        </p>
      )}

      {/* The controls bar, in full and in the corner alike.

          The miniplayer shows a reduced set of the same controls rather than a
          second set of its own: volume and captions are not the simple toggles
          they look like — they duck for narration, and they have to be carried
          across the layer swap by hand — and two implementations of that would
          only agree until the first time one of them was fixed. The mobile bar
          is the exception; it is 72px of mostly text and has its own two buttons.

          What the corner drops is what will not fit or cannot mean anything
          there: the seek row, the clock, next, autoplay, quality, narration, and
          fullscreen. */}
      {variant !== 'bar' && (
      <div
        data-player-controls
        // Fades with the drag. `pointer-events` go the moment the fade starts:
        // a control at 20% opacity is not a control, and a finger travelling
        // over it should not be able to press it by accident.
        style={morph > 0 ? { opacity: 1 - morph, pointerEvents: 'none' } : undefined}
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
        onFocusCapture={() => wakeControls()}
      >
        {variant === 'full' && (
        <SeekBar
          position={position}
          duration={duration}
          buffered={buffered}
          // Seeking works on every tier. The muxed stream has no index of its
          // own, but it is not moved within: the seek goes down to the
          // progressive rendition, across to the mark, and back up.
          //
          // The bar used to be disabled here, which made the seek code beneath
          // it reachable only by the arrow keys — so the path that was broken
          // was also the path nobody could see was broken.
          disabled={!playable}
          // While dragging, only the number moves; the stream is asked for once
          // the handle is released. On a tier that cannot be seeked each of
          // these would otherwise kill an ffmpeg and start another, dozens of
          // times across one drag.
          onScrub={(next) => setPosition(next)}
          onSeek={seekTo}
        />
        )}

        <div className="flex items-center gap-2 py-1.5 text-white">
          <button
            type="button"
            aria-label={playing ? 'Pause' : 'Play'}
            onClick={toggle}
            disabled={!playable}
            className={controlButton}
          >
            {playing ? <Pause size={22} /> : <Play size={22} />}
          </button>

          {/* A real Next button. The autoplay switch sits further along the bar:
              a skip-forward icon means "go to the next video" everywhere else,
              so using it for a toggle made the control look broken. */}
          {onPlayNext && variant === 'full' && (
            <button
              type="button"
              aria-label="Next video"
              title={nextVideoTitle ? `Next: ${nextVideoTitle}` : 'Next video'}
              onClick={() => {
                resetAutoplayChain()
                setCountdown(null)
                onPlayNext()
              }}
              className={controlButton}
            >
              <SkipForward size={22} />
            </button>
          )}

          {!coarse && (
          <VolumeControl
            volume={muted ? 0 : volume}
            muted={muted}
            disabled={!playable}
            onToggleMute={toggleMute}
            onChange={applyVolume}
          />
          )}

          {variant === 'full' && (
            <span className="ml-1 text-xs tabular-nums">
              {formatDuration(position)} / {formatDuration(duration)}
            </span>
          )}
          <span className="flex-1" />

          {/* Autoplay lives in the gear now, with the other settings. On the
              bar it was a switch drawn to look like a switch and still read as
              "skip", since the actual skip button sat right beside it. */}

          {/* Only in the corner. In the full player captions live in the gear
              beside narration: two menus a few pixels apart, one for the
              video's own subtitles and one for the translated ones, was the
              arrangement most likely to have someone change the wrong thing.
              The corner player has no gear, so it keeps its button. */}
          {captionsAvailable && !coarse && variant !== 'full' && (
            <CaptionMenu
              tracks={subtitles}
              active={captions}
              onSelect={setCaptions}
              onOpenChange={trackMenu}
            />
          )}

          {/* The headphone button is gone. It was a second control for the same
              setting the gear now holds, and the two disagreed about what
              narration is: the button knew only on and off, while narration has
              four output modes. Turning it "on" from the bar could not say
              whether that meant subtitles, a voice, or both. */}

          {/* Sound has its own button, beside the gear rather than inside it.
              The gear holds settings that belong to this video — which
              rendition, which subtitles, whether to read them aloud — and the
              equaliser and the room belong to the speakers in front of the
              viewer. They also outgrew a menu row: ten sliders, a preamp, four
              rooms and a mix is a panel, and it pushed everything else on the
              gear below the fold on a phone.

              Left of the gear on purpose. The gear stays next to picture-in-
              picture and full screen, which is where a hand already goes for
              it. */}
          {variant === 'full' && (
            <SettingsMenu
              buttonClassName={controlButton}
              sheet={coarse}
              onOpenChange={trackMenu}
              icon={<SlidersVertical size={22} />}
              label="Audio"
              wide
            >
              <EqualizerSetting audio={audio} onChange={setAudio} element={front()} />
            </SettingsMenu>
          )}

          {/* Only offered when there is more than one way to play the video.
              A quality menu over a single source would be a control that
              cannot do anything — unless the gear is also carrying the
              narration settings, which it is whenever this video can be
              narrated, and those are reachable nowhere else.

              The condition came back when the equaliser moved out. It had been
              dropped while the gear carried a setting every video has; without
              it the gear would now open on nothing at all. */}
          {(qualityOptions.length > 1 || coarse || narrationAvailable) &&
            variant === 'full' && (
              <SettingsMenu
                buttonClassName={controlButton}
                sheet={coarse}
                onOpenChange={trackMenu}
              >
                {resolutionRow}
                {subtitleRows}
                {narrationRows}
                {autoplayRow}
                {translateGroup}
              </SettingsMenu>
            )}

          {/* Picture-in-picture, offered only where the browser has it.
              On iOS this is the *only* way playback survives leaving the page:
              the system suspends video and audio alike when the tab goes to the
              background, and a floating window is the one thing it keeps. */}
          {variant === 'full' && pipAvailable && (
            <button
              type="button"
              aria-label="Picture in picture"
              onClick={() => enterPiP(front())}
              disabled={!playable}
              className={controlButton}
            >
              <PictureInPicture2 size={20} />
            </button>
          )}

          {variant === 'full' && fullscreenAvailable && (
            <button
              type="button"
              aria-label="Full screen"
              onClick={() => goFullscreen(front())}
              disabled={!playable}
              className={controlButton}
            >
              <Maximize size={22} />
            </button>
          )}
        </div>
      </div>
      )}

      {/* Miniplayer overlay — close, expand, and the title. The playback
          controls are the real bar above, not part of this. */}
      {variant === 'mini' && (
        <>
          {/* Deliberately not wrapped in a container.

              These three were grouped in an `absolute inset-0` div, which did
              nothing except hold them — and swallowed every press on the
              controls bar underneath, because a transparent element still takes
              hit tests across everything it covers and this one came later in
              the DOM. They are all absolutely positioned against the player
              root, so the wrapper bought nothing and cost the controls.

              Leaving it out is the fix rather than adding pointer-events-none:
              with nothing spanning the surface there is no press to lose, and
              nobody has to remember a class to keep it that way. */}

          {/* Click the picture to go back to Watch — the picture, and not the
              row of controls along the bottom. The 56px comes from that bar:
              `pt-8 pb-2` around a `py-1.5` row of `h-9` buttons is 56px of
              actual controls. Two numbers in two files with nothing tying them
              together, so changing the bar's padding means changing this. */}
          <button
            type="button"
            onClick={onExpand}
            className="absolute inset-x-0 top-0 bottom-14"
            aria-label="Expand player"
          />

          {/* Close button — top-right, visible on hover */}
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); onClose?.() }}
            className={clsx(
              'absolute top-2 right-2 z-10 grid h-8 w-8 place-items-center rounded-full bg-black/70',
              'transition-opacity duration-150',
              controlsVisible ? 'opacity-100' : 'pointer-events-none opacity-0',
            )}
            aria-label="Close player"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2" strokeLinecap="round">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>

          {/* Expand button — top-left, visible on hover */}
          <button
            type="button"
            onClick={onExpand}
            className={clsx(
              'absolute top-2 left-2 z-10 grid h-8 w-8 place-items-center rounded-full bg-black/70',
              'transition-opacity duration-150',
              controlsVisible ? 'opacity-100' : 'pointer-events-none opacity-0',
            )}
            aria-label="Expand player"
          >
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2" strokeLinecap="round">
              <polyline points="15 3 21 3 21 9" />
              <polyline points="9 21 3 21 3 15" />
              <line x1="21" y1="3" x2="14" y2="10" />
              <line x1="3" y1="21" x2="10" y2="14" />
            </svg>
          </button>

          {/* The title sits above the controls rather than behind them: the
              gradient strip at the bottom is the real controls bar. Nothing to
              press here, so it takes no hit tests either. */}
          <div
            className={clsx(
              'pointer-events-none absolute inset-x-0 bottom-14 px-3 transition-opacity duration-150',
              controlsVisible ? 'opacity-100' : 'opacity-0',
            )}
          >
            <p className="clamp-1 text-xs text-white drop-shadow">{title ?? ''}</p>
          </div>
        </>
      )}

      {/* Mobile bar. No hover state to hide behind on a touch screen, so both
          buttons are permanently visible and sized to be hit with a thumb. */}
      {(bar || morph > 0) && (
        <div
          className="absolute inset-0 flex items-center"
          // The row is laid out around the thumbnail, so its left inset is the
          // thumbnail's width — the same constant the picture is sized from,
          // rather than a `pl-32` that has to be remembered separately.
          //
          // During a drag this is the shape being arrived at, drawn at the
          // opacity the movement has reached. At rest in the corner it is
          // simply the bar, at full strength.
          style={{
            paddingLeft: BAR_THUMB_WIDTH,
            ...(bar ? undefined : { opacity: morph, pointerEvents: 'none' }),
          }}
        >
          <button
            type="button"
            onClick={onExpand}
            className="absolute inset-0"
            aria-label="Expand player"
          />

          <div className="min-w-0 flex-1 px-3">
            <p className="clamp-1 text-xs font-medium text-white">{title ?? ''}</p>
            {channelTitle && <p className="clamp-1 text-[11px] text-text-2">{channelTitle}</p>}
          </div>

          <div data-player-controls className="relative z-10 flex items-center gap-1 pr-2">
            <button
              type="button"
              onClick={toggle}
              className="grid h-10 w-10 place-items-center rounded-full text-white"
              aria-label={playing ? 'Pause' : 'Play'}
            >
              {playing ? <Pause size={20} /> : <Play size={20} />}
            </button>
            <button
              type="button"
              onClick={onClose}
              className="grid h-10 w-10 place-items-center rounded-full text-white"
              aria-label="Close player"
            >
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2" strokeLinecap="round">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>
        </div>
      )}
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
/**
 * A panel behind a button in the control bar.
 *
 * It used to be the quality menu with everything else bolted underneath, which
 * is why quality was a list of rows while every setting added later was a
 * segmented control. It renders what it is handed now, and quality is one group
 * among four rather than the one the frame was built around.
 *
 * Now that it is handed a button as well, it is no longer "the gear" — the
 * sound settings have one of their own beside it. Everything that made this
 * worth reusing is below the button: the portal out of the player's
 * `overflow-hidden`, the measured anchoring, the dropdown-or-sheet split, and
 * the outside-press handling that has to know the list is not inside the ref.
 */
function SettingsMenu({
  onOpenChange,
  buttonClassName,
  children,
  sheet,
  icon,
  label = 'Settings',
  wide,
}: {
  buttonClassName?: string
  children?: React.ReactNode
  /** Render as a sheet at the foot of the screen rather than a dropdown. */
  sheet?: boolean
  /** Lets the player keep its chrome up while this is open. */
  onOpenChange: (open: boolean) => void
  /** What the button shows. The gear when nothing says otherwise. */
  icon?: React.ReactNode
  /** Names the button for a screen reader, and says which panel this is. */
  label?: string
  /**
   * A wider dropdown, for a panel that is a row of controls rather than a list.
   *
   * The default 18rem was measured for menu rows, where the width is set by the
   * longest label. Ten equaliser bands across it leave 25px each — narrower on
   * a desktop than the same ten get on a phone, which is the wrong way round.
   */
  wide?: boolean
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLUListElement>(null)
  useEffect(() => {
    onOpenChange(open)
    return () => { if (open) onOpenChange(false) }
  }, [open, onOpenChange])
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node
      // The sheet is portalled out of the player, so it is not inside `ref`.
      // Without asking it too, pressing anything on it would count as pressing
      // outside and close it before the press arrived.
      if (ref.current?.contains(target)) return
      if (listRef.current?.contains(target)) return
      setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])

  const buttonRef = useRef<HTMLButtonElement>(null)
  const [menuPos, setMenuPos] = useState<{ bottom: number; right: number } | null>(null)

  // Measure the button's viewport position so the portalled dropdown can sit
  // exactly where it would have been, just outside the clipping container.
  useEffect(() => {
    if (!open || sheet) return
    const btn = buttonRef.current
    if (!btn) return
    const measure = () => {
      const r = btn.getBoundingClientRect()
      setMenuPos({
        bottom: window.innerHeight - r.top, // distance from bottom edge
        right: window.innerWidth - r.right, // distance from right edge
      })
    }
    measure()
    window.addEventListener('resize', measure)
    window.addEventListener('scroll', measure, { passive: true })
    return () => {
      window.removeEventListener('resize', measure)
      window.removeEventListener('scroll', measure)
      setMenuPos(null)
    }
  }, [open, sheet])

  return (
    <div ref={ref} className="relative">
      <button
        ref={buttonRef}
        type="button"
        aria-label={label}
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className={
          buttonClassName ??
          'grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10'
        }
      >
        {icon ?? <Settings size={22} />}
      </button>

      {/* The menu is portalled to document.body regardless of mode, because
          the player host clips its contents with overflow-hidden (it has to,
          to keep the video's rounded corners). On desktop the dropdown opens
          upwards from the button; on mobile it is a bottom sheet. Both are
          clipped by the player unless portalled out. */}
      {open &&
        createPortal(
          !sheet && menuPos ? (
            // Desktop: dropdown anchored to the button, opening upwards.
            <ul
              ref={listRef}
              // `panel-blur`, not `chrome-blur`. The first attempt used the
              // bar's class and the blur was invisible: at 95% only a
              // twentieth of what is behind comes through, which is the point
              // for a permanent bar and the opposite of it for a panel opened
              // over video. See index.css for why the alpha stops at 82%.
              className={clsx(
                'panel-blur fixed z-[60] max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg py-1 text-sm shadow-lg',
                wide ? 'w-96' : 'w-72',
              )}
              style={{
                bottom: `${menuPos.bottom}px`,
                right: `${menuPos.right}px`,
              }}
            >
              {children}
            </ul>
          ) : sheet ? (
            // Mobile: bottom sheet with a scrim.
            <>
              {/* The scrim sits behind the sheet as well as beside it, so it
                  is part of what the sheet blurs: at 50% it took half of what
                  little the panel already lets through and the frosting stopped
                  reading at all. 40% still separates the sheet from the video
                  and still says "press here to dismiss".

                  Lowering it cannot hurt the panel's contrast — a darker
                  backdrop only ever helps, and the 82% alpha was chosen against
                  no scrim at all. */}
              <div
                className="fixed inset-0 z-[60] bg-black/40"
                onClick={() => setOpen(false)}
                aria-hidden
              />
              <ul
                ref={listRef}
                className="panel-blur fixed inset-x-0 bottom-0 z-[60] max-h-[70vh] overflow-y-auto rounded-t-2xl pt-2 text-sm shadow-2xl"
                style={{ paddingBottom: 'calc(0.5rem + var(--safe-bottom))' }}
              >
                {children}
              </ul>
            </>
          ) : null,
          document.body,
        )}
    </div>
  )
}

/**
 * One on/off line in the settings sheet.
 *
 * A row rather than an icon, because the sheet has room for words and the bar
 * did not — which is the reason these moved here.
 */
/**
 * What the translator is doing right now.
 *
 * The batch engine works ahead of the playhead for minutes and makes no sound
 * while it does, so "still translating" and "translation is broken" present
 * identically: nothing happens. This is the only place either state is
 * visible. The realtime engine has nothing to report — it translates a line at
 * the moment it speaks it — so it says so rather than showing an empty bar.
 */
/**
 * How far the voice has been prepared.
 *
 * The companion to NarrationStatus above, and deliberately its twin rather than
 * a different-looking thing: they are two stages of one pipeline — lines are
 * translated, then spoken — and a viewer reads them together to answer one
 * question, which is when they will actually hear something.
 *
 * Worth showing at all because preparation now covers the whole video rather
 * than the next few seconds. That is minutes of work on a long video, it carries
 * on while the video is paused, and without a report the only evidence of it is
 * a fan spinning up.
 */
function SpeechStatus({
  progress: p,
}: {
  progress: ReturnType<typeof pregenProgress>
}) {
  // Every phase gets its own words, for the reason recorded on the translation
  // status below: several distinct states behind one hopeful label is how
  // "stuck on preparing" gets reported. In particular a sweep waiting on the
  // translator and a sweep waiting out a dead synthesiser look identical from
  // the outside and want completely different responses from the viewer.
  const label: Record<typeof p.phase, string> = {
    // Every label names its subject. The row above reports translation and this
    // one reports speech, and a bare "Not started" on both left two identical
    // lines stacked on each other with nothing to say which was which.
    idle: 'Speech not started',
    sweeping: 'Preparing speech…',
    'awaiting-translation': 'Waiting for translation…',
    'backing-off': 'Speech service unavailable — retrying',
    done: 'Speech ready',
  }
  const bar = p.total > 0 && p.phase !== 'idle'
  const pct = p.total > 0 ? Math.round((p.done / p.total) * 100) : 0

  return (
    <li className="px-4 py-3" aria-live="polite">
      <div className="flex items-baseline justify-between gap-2 pb-1 text-xs">
        <span
          className={p.phase === 'backing-off' ? 'text-brand' : 'text-text-2'}
        >
          {label[p.phase]}
        </span>
        {bar && (
          <span className="tabular-nums text-text-2">
            {p.done}/{p.total} lines
          </span>
        )}
      </div>
      {p.etaSeconds !== null && (
        <div className="pb-1 text-xs text-text-2">
          {formatEta(p.etaSeconds)} left
        </div>
      )}
      {/*
        Lines that cannot be said in the time they have, whatever the tempo.
        Reported rather than hidden: a handful is normal, but a video full of
        them means the translations are running long, and that is fixed in the
        prompt rather than anywhere the viewer can reach. Without a count it
        would show up as narration that skips lines for no visible reason —
        which is exactly the complaint this whole change set began with.
      */}
      {p.tooFast > 0 && (
        <div className="pb-1 text-xs text-text-2">
          {p.tooFast} line{p.tooFast === 1 ? '' : 's'} too long to speak in time
        </div>
      )}
      {bar && (
        <div className="h-1 overflow-hidden rounded-full bg-white/15">
          <div
            className={clsx(
              'h-full rounded-full transition-[width] duration-300 ease-out',
              p.phase === 'done' ? 'bg-white/50' : 'bg-brand',
            )}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
    </li>
  )
}

function NarrationStatus({
  progress: p,
}: {
  progress: ReturnType<typeof narrationProgress>
}) {
  // "Preparing" was true of a pass that had not started, one waiting on a
  // subtitle file, one whose subtitles never arrived, and one with nothing to
  // do because the cues were already Vietnamese. Four states behind one word is
  // no better than no status at all — it was reported as "stuck on preparing".
  const label: Record<typeof p.phase, string> = {
    idle: 'Not started',
    // Each step before the first batch says which step it is. One word over all
    // of them meant a pass held up by the translator settings — or by hashing a
    // long video's cues — claimed to be loading subtitles that were already on
    // screen, and there was no way to tell which from the outside.
    'waiting-config': 'Waiting for translator settings…',
    'no-translator': 'No translation model configured — set one in Settings',
    'reading-cache': 'Reading saved translations…',
    'waiting-subtitles': 'Loading subtitles…',
    hashing: 'Preparing cues…',
    'no-subtitles': 'No subtitles available',
    'not-needed': 'Already Vietnamese — nothing to translate',
    translating: 'Translating…',
    done: 'Translated',
    failed: p.error
      ? `Translation failed: ${p.error}`
      : 'Translation failed — nothing came back',
  }
  const bar =
    p.phase === 'translating' || p.phase === 'done' || p.phase === 'failed'
  const pct = p.total > 0 ? Math.round((p.done / p.total) * 100) : 0

  return (
    // The same `px-4 py-3` every other row in this panel uses. It had only
    // bottom padding, so it sat flush against the divider above it while every
    // switch beside it stood 12px clear — the panel read as though this line
    // belonged to the group before it rather than being its own thing.
    <li className="px-4 py-3" aria-live="polite">
      <div className="flex items-baseline justify-between gap-2 pb-1 text-xs">
        <span
          className={
            p.phase === 'no-subtitles' || p.phase === 'no-translator'
              ? 'text-brand'
              : 'text-text-2'
          }
        >
          {label[p.phase]}
        </span>
        {bar && (
          <span className="tabular-nums text-text-2">
            {p.done}/{p.total} lines
          </span>
        )}
      </div>
      {p.etaSeconds !== null && (
        <div className="pb-1 text-xs text-text-2">
          {formatEta(p.etaSeconds)} left
        </div>
      )}
      {bar && (
        <div className="h-1 overflow-hidden rounded-full bg-white/15">
          <div
            className={clsx(
              'h-full rounded-full transition-[width] duration-300 ease-out',
              p.phase === 'done' ? 'bg-white/50' : 'bg-brand',
            )}
            style={{ width: `${pct}%` }}
          />
        </div>
      )}
    </li>
  )
}

/**
 * One choice out of a few, laid out as a segmented control.
 *
 * Six switches were six independent yes/no questions to read, when narration
 * only ever has one output mode and one engine — a switch invites you to work
 * out which combinations are legal, a segment shows there is exactly one answer
 * and what the alternatives are. Matches the Like/Dislike pair in the design
 * system (MASTER.md), which is segmented for the same reason.
 */
function SegmentedSetting<T extends string>({
  label,
  options,
  value,
  onSelect,
  tall,
}: {
  label: string
  options: Array<{ value: T; label: string; hint?: string }>
  value: T
  onSelect: (v: T) => void
  /** Touch targets need 44px; a mouse is happy with less. */
  tall?: boolean
}) {
  return (
    <li className="px-4 py-2">
      <div className="pb-1.5 text-xs text-text-2">{label}</div>
      <div
        role="radiogroup"
        aria-label={label}
        className="flex overflow-hidden rounded-lg bg-white/10 p-0.5"
      >
        {options.map((o) => {
          const on = o.value === value
          return (
            <button
              key={o.value}
              type="button"
              role="radio"
              aria-checked={on}
              title={o.hint}
              onClick={() => onSelect(o.value)}
              className={clsx(
                'flex-1 rounded-md px-2 text-center text-xs font-medium transition-colors duration-150 ease-out',
                tall ? 'py-2.5' : 'py-1.5',
                on
                  ? 'bg-invert-bg text-invert-text'
                  : 'text-text-2 hover:bg-white/10 hover:text-text',
              )}
            >
              {o.label}
            </button>
          )
        })}
      </div>
    </li>
  )
}

function SettingRow({
  label,
  on,
  onToggle,
}: {
  label: string
  on: boolean
  onToggle: () => void
}) {
  return (
    <li>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        onClick={onToggle}
        // No hover fill. The switch beside the label is the feedback — it slides
        // and changes colour on press — and a row that lit up under the pointer
        // as well said the same thing twice. `transition-colors` goes with it:
        // nothing on this row changes colour any more.
        className="flex w-full items-center justify-between gap-4 px-4 py-3 text-left"
      >
        <span>{label}</span>
        <span
          className={clsx(
            'flex h-4 w-8 shrink-0 items-center rounded-full px-0.5 transition-colors duration-150 ease-out',
            on ? 'bg-brand' : 'bg-white/25',
          )}
        >
          <span
            className={clsx(
              'h-3 w-3 rounded-full bg-white transition-transform duration-150 ease-out',
              on && 'translate-x-4',
            )}
          />
        </span>
      </button>
    </li>
  )
}

/**
 * Captions for the corner player, which has no gear.
 *
 * In the full player captions moved into the settings menu beside narration;
 * here there is no settings menu to move them into, and the corner player is
 * the one shape where a viewer is most likely to be half-watching something in
 * another language. So this button survives, in this shape only.
 */
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
  const ref = useRef<HTMLDivElement>(null)
  const listRef = useRef<HTMLUListElement>(null)
  useEffect(() => {
    onOpenChange(open)
    return () => { if (open) onOpenChange(false) }
  }, [open, onOpenChange])
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node
      // The sheet is portalled out of the player, so it is not inside `ref`.
      // Without asking it too, pressing anything on it would count as pressing
      // outside and close it before the press arrived.
      if (ref.current?.contains(target)) return
      if (listRef.current?.contains(target)) return
      setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [open])
  const Icon = active ? Captions : CaptionsOff

  return (
    <div ref={ref} className="relative">
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

/**
 * What to say about a video YouTube will not hand over.
 *
 * Each reason gets its own sentence because each one leads somewhere different:
 * a members-only video can be unlocked by joining the channel, a removed one is
 * gone for everybody. "Could not be fetched" sends the viewer nowhere, which is
 * what the 500 it replaced did.
 */
export function unavailableCopy(reason: UnavailableReason): string {
  switch (reason) {
    case 'members_only':
      return 'This video is members-only on YouTube. Join the channel there to watch it — it cannot be fetched into the library.'
    case 'private':
      return 'This video is private on YouTube, so it cannot be fetched.'
    case 'removed':
      return 'This video has been removed from YouTube, so it cannot be fetched.'
    default:
      return 'YouTube will not hand this video over, so it cannot be fetched.'
  }
}
