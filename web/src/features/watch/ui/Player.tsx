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
  Volume1,
  Volume2,
  VolumeX,
} from 'lucide-react'
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
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
import {
  bindNarration,
  tickNarration,
  resetNarration,
  loadViSubtitles,
  setNarrationEngine,
  setNarrationVideo,
  startTranslationPass,
  narrationProgress,
} from '@/features/watch/application/narration'
import {
  loadNarrationPrefs,
  saveNarrationPrefs,
  type NarrationOutput,
} from '@/features/watch/application/narration-prefs'
import type { NarrationEngine } from '@/features/watch/infrastructure/narration-cache'
import { NarrationSubtitles } from '@/features/watch/ui/NarrationSubtitles'
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
  // Cache-bust so the browser never serves a stale stream when seeking.
  return `${tier.url}?t=${offsetSeconds.toFixed(3)}&_=${Date.now()}`
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
  thumbnailURL,
  nextVideoTitle,
  onPlayNext,
  title,
  channelTitle,
  variant = 'full',
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
  const narrationOn = narrationPrefs.output !== 'off'
  // Showing the translation and speaking it are separate: subtitles must not
  // duck the video's own audio, and must not start the TTS scheduler.
  const narrationSpeaks =
    narrationPrefs.output === 'voice' || narrationPrefs.output === 'both'
  const narrationShows =
    narrationPrefs.output === 'subs' || narrationPrefs.output === 'both'
  const narrationOnRef = useRef(false)
  const audioCtxRef = useRef<AudioContext | null>(null)
  // Keep refs synchronised so callbacks that are intentionally stable (empty
  // dependency arrays) never read a stale closure value — particularly
  // handoverToBack, which copies text track modes across the swap.
  useEffect(() => { captionsRef.current = captions }, [captions])
  useEffect(() => { narrationOnRef.current = narrationOn }, [narrationOn])
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
  const pendingTierRef = useRef<{ tier: Tier; offset: number } | undefined>(undefined)
  // Absolute position in the video, offset included. Read by the effect that
  // opens a muxed stream, which needs to know where the viewer is without
  // taking position as a dependency and restarting on every tick.
  const positionRef = useRef(0)
  const handoverFrameRef = useRef(0)
  // The volume we last set programmatically (for ducking).  When the browser
  // fires onVolumeChange asynchronously, comparing against this value tells us
  // whether the event came from our own effect or from a real user interaction.
  const lastSetVolumeRef = useRef(-1)
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
  // Narration is available when there are Vietnamese subtitles. We don't know
  // until the <track> elements load, so we check via hasVietnameseSubs().
  // Narration is available when there are Vietnamese or English subtitles.
  // English cues are translated via NLLB-200 before TTS.
  const narrationAvailable = subtitles.some(
    (s) => s.language === 'vi' || s.language === 'vie' || s.language === 'en' || s.language === 'eng',
  )

  // <track> elements are created synchronously by React, but the browser
  // initialises the backing TextTrack objects asynchronously (microtask).
  // useLayoutEffect runs before that — textTracks.length is 0 on first fire.
  // Poll with rAF until the tracks are ready, then apply the stored preference.
  useLayoutEffect(() => {
    const element = front()
    if (!element) return
    let frame = 0
    const apply = () => {
      if (element.textTracks.length === 0) {
        frame = requestAnimationFrame(apply)
        return
      }
      for (let i = 0; i < element.textTracks.length; i++) {
        const track = element.textTracks[i]
        track.mode = track.language === captionsRef.current ? 'showing' : 'disabled'
      }
    }
    frame = requestAnimationFrame(apply)
    return () => cancelAnimationFrame(frame)
  }, [captions, frontSrc, frontIsA, front, subtitles.length])

  // Create AudioContext when narration activates.  Must happen here — not just
  // in onClick — because narrationOn can be restored from localStorage on page
  // load without any user gesture.
  useEffect(() => {
    if (narrationOn && !audioCtxRef.current) {
      audioCtxRef.current = new AudioContext()
    }
  }, [narrationOn])

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
    const element = front()
    const unbind = element ? bindNarration(element) : undefined

    const id = setInterval(() => {
      const el = front()
      if (!el || !audioCtxRef.current) return
      tickNarration(el, audioCtxRef.current)
    }, 100)

    return () => {
      clearInterval(id)
      unbind?.()
      resetNarration()
      audioCtxRef.current?.suspend()
    }
  }, [narrationSpeaks, front])

  // Restore stored volume/muted on the video element, and duck the video
  // audio when narration is active so the TTS voice is clearly audible.
  // Duck video audio while narration is active so the TTS voice is clear.
  // Only applies when the video actually has Vietnamese subtitles — otherwise
  // narrationOn could be stuck true from localStorage with no button to turn
  // it off, permanently halving the volume.
  const NARRATION_DUCK = 0.2
  const ducking = narrationSpeaks && narrationAvailable
  useEffect(() => {
    const el = front()
    if (!el) return
    const target = ducking ? volume * NARRATION_DUCK : volume
    lastSetVolumeRef.current = target
    el.muted = muted
    el.volume = target
  }, [volume, muted, ducking, frontSrc])

  // Reset narration state when moving to a new video.
  useEffect(() => { resetNarration() }, [videoId])

  // When narration is turned on, fetch and parse the best available VTT:
  // Vietnamese first, then English (which will be translated via NLLB-200).
  useEffect(() => {
    if (!narrationOn) return
    const viSub = subtitles.find(
      (s) => s.language === 'vi' || s.language === 'vie',
    )
    if (viSub) { loadViSubtitles(viSub.url, 'vi'); return }
    const enSub = subtitles.find(
      (s) => s.language === 'en' || s.language === 'eng',
    )
    if (enSub) loadViSubtitles(enSub.url, 'en')
  }, [narrationOn, subtitles])

  // The engine lives in two places — React state for the menu, a module
  // variable for the code that translates — and only a click was keeping them
  // in step. A preference restored from localStorage on load never reached the
  // module, so a viewer who had chosen NLLB came back to a menu saying NLLB
  // while the batch engine did the work.
  useEffect(() => {
    setNarrationEngine(narrationPrefs.engine)
  }, [narrationPrefs.engine])

  // Tell narration which video it is for, so synthesised clips are filed beside
  // that video. Not folded into the translation pass: the realtime engine has
  // no pass, and its clips are worth keeping too.
  useEffect(() => {
    setNarrationVideo(videoId)
  }, [videoId])

  // Anchor the background translation pass wherever the viewer actually is.
  // Only the batch engine has a pass; NLLB translates as it speaks.
  useEffect(() => {
    if (!narrationOn || narrationPrefs.engine !== 'qwen') return
    const el = front()
    startTranslationPass(videoId, el ? el.currentTime : 0)
  }, [narrationOn, narrationPrefs.engine, videoId, subtitles, front])

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
    if (!current || !next) return
    // When the replacement was opened at a substantially different offset
    // than the current stream, this is a seek rather than an upgrade. The
    // playhead will never catch the new stream — it starts at a different
    // moment entirely — so commit as soon as there is data to show.
    const isSeek =
      pendingTierRef.current !== undefined &&
      Math.abs(pendingTierRef.current.offset - offsetRef.current) > 1
    // upgradingToRef may have been cleared before onLoadedMetadata fires,
    // so for seeks we use pendingTierRef as the signal instead.
    if (!upgradingToRef.current && !isSeek) return

    const commit = () => {
      // Carry across everything the viewer set, or the swap would silently undo
      // their volume, their mute and their subtitles.
      next.volume = current.volume
      next.muted = current.muted
      for (let i = 0; i < next.textTracks.length; i++) {
        const track = next.textTracks[i]
        const isVi = track.language === 'vi' || track.language === 'vie'
        // Never disable the Vietnamese track when narration is on: the
        // browser would cancel the VTT load and the viewer would lose the
        // thuyết minh audio mid-sentence during the upgrade.
        if (isVi && narrationOnRef.current) {
          track.mode = 'hidden'
          continue
        }
        track.mode = track.language === captionsRef.current ? 'showing' : 'disabled'
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
      if (isSeek) {
        if (next.buffered.length > 0 && next.buffered.end(0) >= 0.5) {
          commit()
          return
        }
        handoverFrameRef.current = window.requestAnimationFrame(waitForMark)
        return
      }
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
  const setNarrationOutput = useCallback((output: NarrationOutput) => {
    // The AudioContext has to be created and resumed inside the gesture, or the
    // browser's autoplay policy will not let it make a sound.
    if (output === 'voice' || output === 'both') {
      if (!audioCtxRef.current) audioCtxRef.current = new AudioContext()
      void audioCtxRef.current.resume()
    }
    setNarrationPrefs((p) => {
      const next = { ...p, output }
      saveNarrationPrefs(next)
      return next
    })
  }, [])

  const setEngine = useCallback((engine: NarrationEngine) => {
    setNarrationPrefs((p) => {
      const next = { ...p, engine }
      saveNarrationPrefs(next)
      return next
    })
    // Drops the cached answers of the engine being left behind, so the two are
    // never blended in the comparison this menu exists for.
    setNarrationEngine(engine)
  }, [])

  /**
   * The narration group of the settings menu.
   *
   * Built once and used by both pointer types. It first went only into the
   * touch branch of `extras`, which meant a mouse never saw it: with a mouse
   * the bar has room for its own captions and narration buttons, so that branch
   * renders nothing. The engine choice had nowhere to appear at all.
   */
  // Declared here rather than beside the other layout state: the narration
  // settings below size their touch targets from it, and they are built first.
  const coarse = useCoarsePointer()

  /**
   * The video's own subtitles. Switches rather than a segmented control: the
   * list is however many languages the video shipped with, which is not a fixed
   * small set, and a segment that has to wrap is worse than a list that scrolls.
   */
  const captionRows = captionsAvailable ? (
    <SegmentedSetting
      label="Phụ đề gốc"
      value={captions ?? 'off'}
      onSelect={(v) => setCaptions(v === 'off' ? null : v)}
      tall={coarse}
      options={[
        { value: 'off', label: 'Tắt' },
        // Only the two languages this library actually narrates between. A
        // video carrying eight tracks would otherwise wrap the control onto
        // three rows, which reads worse than the list it replaced.
        ...subtitles
          .filter((t) => /^(en|eng|vi|vie)$/.test(t.language))
          .map((t) => ({
            value: t.language,
            label: /^en/.test(t.language) ? 'EN' : 'VI',
            hint: t.label + (t.generated ? ' (tự động)' : ''),
          })),
      ]}
    />
  ) : undefined

  const narrationRows = narrationAvailable ? (
    <>
      <SegmentedSetting
        label="Thuyết minh tiếng Việt"
        value={narrationPrefs.output}
        onSelect={setNarrationOutput}
        tall={coarse}
        options={[
          { value: 'off', label: 'Tắt' },
          { value: 'subs', label: 'Phụ đề', hint: 'Hiện bản dịch, không đọc' },
          { value: 'voice', label: 'Giọng đọc', hint: 'Đọc thành tiếng' },
          { value: 'both', label: 'Cả hai' },
        ]}
      />
      {narrationPrefs.output !== 'off' && (
        <>
          <SegmentedSetting
            label="Máy dịch"
            value={narrationPrefs.engine}
            onSelect={setEngine}
            tall={coarse}
            options={[
              { value: 'qwen', label: 'Kỹ hơn', hint: 'Qwen — dịch nền theo lô, có ngữ cảnh' },
              { value: 'nllb', label: 'Nhanh hơn', hint: 'NLLB — dịch ngay từng câu' },
            ]}
          />
          <NarrationStatus engine={narrationPrefs.engine} />
        </>
      )}
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
    const element = front()
    setVolume(next)
    window.localStorage.setItem('yt-player-volume', String(next))
    window.localStorage.setItem(MUTED_KEY, next === 0 ? '1' : '0')
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
    const high = tiers.find((t) => t.name === 'local' || t.name === 'remux')
    if (high) options.push({ value: 'high', label: `${high.height ?? remuxLabelHeight}p` })
    const low = tiers.find((t) => t.name === 'instant')
    if (low) options.push({ value: 'low', label: `${low.height ?? 360}p` })
    return options
  }, [tiers])

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
  // stopped, even when it was running on the way in. Leaving a video and
  // returning to a still frame reads as the player having given up.
  //
  // Two things make this harder than it sounds, and the first attempt fell to
  // both. The memory of "it was playing" cannot live in the effect's closure:
  // the source can change while the viewer is in full screen — the downloaded
  // file becoming ready is the obvious way — and the effect re-running takes
  // that memory with it. And the pause does not reliably arrive before the
  // announcement that full screen ended, so checking at that moment can find a
  // video that has not stopped yet and will a moment later.
  //
  // So the memory is a ref, and the window stays open long enough to catch a
  // pause that arrives after the event rather than before it.
  const resumeAfterFullscreenRef = useRef(false)
  useEffect(() => {
    const elements = [videoARef.current, videoBRef.current].filter(
      (el): el is HTMLVideoElement => el !== null,
    )
    if (elements.length === 0) return

    const onBegin = (event: Event) => {
      resumeAfterFullscreenRef.current = !(event.target as HTMLVideoElement).paused
    }
    const onEnd = (event: Event) => {
      if (!resumeAfterFullscreenRef.current) return
      const element = event.target as HTMLVideoElement
      const resume = () => {
        if (element.paused) void element.play().catch(() => undefined)
      }
      resume()
      // And once more after the event queue has drained, for the ordering where
      // iOS stops it on the way out rather than on the way.
      const timer = window.setTimeout(() => {
        resume()
        resumeAfterFullscreenRef.current = false
      }, 250)
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
  }, [playable])

  const downloading = download?.state === 'RUNNING' || download?.state === 'QUEUED'
  const downloadPercent = Math.round((download?.progress ?? 0) * 100)

  return (
    <div
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
      }}
      onPointerDown={(e) => {
        pointerKindRef.current = e.pointerType === 'touch' ? 'touch' : 'mouse'
        wakeControls(pointerKindRef.current)
      }}
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

      <NarrationSubtitles front={front} active={narrationShows} />

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
                className={clsx(
                  'absolute top-0 left-0 h-full cursor-pointer',
                  // In the bar the picture is a thumbnail on the left rather than
                  // the whole surface: stretched across a 72px-tall strip it would
                  // be a smear, and the row is mostly text at that size anyway.
                  bar ? 'w-32 object-cover' : 'w-full',
                )}
                // The hidden layer must stay laid out and decoding — display:none
                // would stop it buffering, which is the entire point of it.
                style={{ opacity: isFront ? 1 : 0, pointerEvents: isFront ? undefined : 'none' }}
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
                  if (isA === frontIsARef.current) setPlaying(true)
                }}
                onPause={(e) => {
                  // A pause arriving in the moments after full screen ended is
                  // the system letting go, not the viewer stopping the video.
                  if (resumeAfterFullscreenRef.current) {
                    resumeAfterFullscreenRef.current = false
                    void e.currentTarget.play().catch(() => undefined)
                    return
                  }
                  if (isA === frontIsARef.current) setPlaying(false)
                }}
                onVolumeChange={
                  isFront
                    ? (e) => {
                        // Follow the element, but do not record what it says.
                        //
                        // This event fires for our own changes as much as for
                        // the viewer's — ducking for narration, restoring after
                        // it, and once upon a time the autoplay policy muting
                        // the video on load. The guard below only compares
                        // volume, so a change to `muted` alone got through, and
                        // early in a page load `lastSetVolumeRef` is still -1
                        // and even that guard does not hold. A mute nobody
                        // asked for was therefore written down as a preference
                        // and read back on every later visit, which is why the
                        // sound stayed off long after the code that muted it
                        // had gone.
                        //
                        // Preferences are written where the viewer expresses
                        // them: the mute button and the volume slider.
                        if (e.currentTarget.volume === lastSetVolumeRef.current) return
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
                  if (autoplay) element.play().catch(() => undefined)
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

          {onPlayNext && variant === 'full' && !coarse && (
            <button
              type="button"
              role="switch"
              aria-checked={autoplayEnabled}
              aria-label="Autoplay"
              title={autoplayEnabled ? 'Autoplay is on' : 'Autoplay is off'}
              onClick={() => setAutoplayEnabled(!autoplayEnabled)}
              className={controlButton}
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

          {/* Only offered when there is more than one way to play the video.
              A quality menu over a single source would be a control that
              cannot do anything — unless the gear is also carrying the
              narration settings, which it is whenever this video can be
              narrated, and those are reachable nowhere else. */}
          {(qualityOptions.length > 1 || coarse || narrationAvailable) &&
            variant === 'full' && (
            <QualityMenu
              choice={quality}
              options={qualityOptions}
              playingLabel={tierLabel}
              buttonClassName={controlButton}
              sheet={coarse}
              onOpenChange={trackMenu}
              // On a phone the gear is the only place left for the settings the
              // bar no longer has room to show one by one.
              extras={
                coarse ? (
                  <>
                    {captionRows}
                    {narrationRows}
                    {onPlayNext && (
                      <SettingRow
                        label="Tự động phát"
                        on={autoplayEnabled}
                        onToggle={() => setAutoplayEnabled(!autoplayEnabled)}
                      />
                    )}
                  </>
                ) : (
                  <>
                    {captionRows}
                    {narrationRows}
                  </>
                )
              }
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
      {bar && (
        <div className="absolute inset-0 flex items-center pl-32">
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
function QualityMenu({
  choice,
  options,
  playingLabel,
  onSelect,
  onOpenChange,
  buttonClassName,
  extras,
  sheet,
}: {
  choice: QualityChoice
  options: { value: QualityChoice; label: string }[]
  buttonClassName?: string
  /** Extra rows below the quality list. On a phone this is where the settings
   *  the bar no longer has room for end up. */
  extras?: React.ReactNode
  /** Render as a sheet at the foot of the screen rather than a dropdown. */
  sheet?: boolean
  /** What is on screen right now, shown beside Auto so it is never a mystery. */
  playingLabel: string
  onSelect: (next: QualityChoice) => void
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

  // One list, two frames around it.
  const rows = options.map((option) => (
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
  ))

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-label="Settings"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className={
          buttonClassName ??
          'grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10'
        }
      >
        <Settings size={22} />
      </button>

      {open && !sheet && (
        <ul
          ref={listRef}
          // Wider than it was: the segmented controls inside need room for four
          // labels side by side, and the progress line needs room for a status
          // and a count on one row. min-w-44 squeezed both onto two lines each.
          className="absolute right-0 bottom-11 w-72 max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg bg-surface py-1 text-sm shadow-lg"
        >
          {rows}
          {extras && <li className="my-1 border-t border-line" aria-hidden />}
          {extras}
        </ul>
      )}

      {/* On a phone the same list is a sheet at the foot of the screen, and it
          is portalled out of the player to get there.

          Not a style choice. The player clips its own contents — it has to, so
          the picture keeps its corners — and on a phone it is only about two
          hundred pixels tall. A dropdown opening upwards from the bar inside
          that box has nowhere to go: the top of the list is simply cut off,
          which is how this was reported. Rendering into the body puts it
          outside the box that was doing the cutting. */}
      {open && sheet &&
        createPortal(
          <>
            <div
              className="fixed inset-0 z-[60] bg-black/50"
              onClick={() => setOpen(false)}
              aria-hidden
            />
            <ul
              ref={listRef}
              className="fixed inset-x-0 bottom-0 z-[60] max-h-[70vh] overflow-y-auto rounded-t-2xl bg-surface pt-2 text-sm shadow-2xl"
              style={{ paddingBottom: 'calc(0.5rem + var(--safe-bottom))' }}
            >
              {rows}
              {extras && <li className="my-1 border-t border-line" aria-hidden />}
              {extras}
            </ul>
          </>,
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
function NarrationStatus({ engine }: { engine: NarrationEngine }) {
  const [p, setP] = useState(narrationProgress)

  useEffect(() => {
    const id = window.setInterval(() => setP(narrationProgress()), 500)
    return () => window.clearInterval(id)
  }, [])

  if (engine !== 'qwen') {
    return (
      <li className="px-4 pb-2 text-xs text-text-2">
        Dịch từng câu ngay khi đọc, không chạy nền.
      </li>
    )
  }

  // "Preparing" was true of a pass that had not started, one waiting on a
  // subtitle file, one whose subtitles never arrived, and one with nothing to
  // do because the cues were already Vietnamese. Four states behind one word is
  // no better than no status at all — it was reported as "stuck on preparing".
  const label: Record<typeof p.phase, string> = {
    idle: 'Chưa bắt đầu',
    'waiting-subtitles': 'Đang tải phụ đề…',
    'no-subtitles': 'Không lấy được phụ đề',
    'not-needed': 'Phụ đề đã là tiếng Việt, không cần dịch',
    translating: 'Đang dịch nền…',
    done: 'Đã dịch xong',
  }
  const bar = p.phase === 'translating' || p.phase === 'done'
  const pct = p.total > 0 ? Math.round((p.done / p.total) * 100) : 0

  return (
    <li className="px-4 pb-3" aria-live="polite">
      <div className="flex items-baseline justify-between gap-2 pb-1 text-xs">
        <span className={p.phase === 'no-subtitles' ? 'text-brand' : 'text-text-2'}>
          {label[p.phase]}
        </span>
        {bar && (
          <span className="tabular-nums text-text-2">
            {p.done}/{p.total} câu
          </span>
        )}
      </div>
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
        className="flex w-full items-center justify-between gap-4 px-4 py-3 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
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
