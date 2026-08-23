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
import { useStream } from '@/features/catalog/application/queries'
import { atLiveEdge, livePercent } from '../application/live-timeline'
import { playbackDuration } from '../application/player-duration'
import { log } from '@/shared/api/log'
import { seekElement } from '@/features/watch/application/player-seek'
import {
  type Tier,
  PINNED_HEIGHT,
  availableTiers,
  openingTier,
  targetTier,
  tierLabel as labelForTier,
} from '@/features/watch/application/player-source'
import {
  attachHLS,
  type HLSAttachment,
  hlsCapabilities,
  canSelectHLSLevel,
  bypassesWebAudio,
  needsHLSLibrary,
} from '@/features/watch/application/hls-source'
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
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import { useTTSConfig } from '@/features/settings/application/queries'

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
 * How many times the climb to the local file may be lost before the player
 * settles for what it already has.
 *
 * A retry is nearly free here — the file is on disk, there is no process and no
 * request upstream — so the limit is not about cost. It is about the drive
 * disappearing (CLAUDE.md §8, risk 1), where every attempt fails identically
 * and forever, and a loop of a video that will not load is the worst possible
 * way to find that out.
 */
/**
 * How long a prepared layer may take to have a frame at the viewer's position
 * before the exchange is given up on.
 *
 * This replaces four constants — a swap lead, a mux patience, an overshoot
 * tolerance and a catch-up margin — which between them encoded guesses about
 * how far ahead to open an unseekable stream and how much lateness to forgive.
 * None of that survives a source the browser can seek: the replacement is put
 * where the viewer already is, so the only question left is whether it has
 * buffered anything there yet.
 *
 * Generous, because being slow is not being broken and the viewer is watching
 * the other layer throughout.
 */
const HANDOVER_PATIENCE_MS = 15_000

const MAX_LOCAL_ATTEMPTS = 3

/**
 * The height the full-quality tiers are labelled with when they do not say.
 *
 * The local file and the muxed stream are both produced at the configured
 * download height, and neither carries it in the stream answer.
 */
const remuxLabelHeight = 1080



/**
 * Is `el` showing the source at `url`?
 *
 * The tier machinery identifies a layer by what it is playing, at three sites:
 * the handover, the claim it acts on, and the failure that abandons it. Each
 * compared `element.src` against the claim's URL.
 *
 * That stops working the moment a source is attached rather than assigned.
 * hls.js hands the element a `blob:` URL of its own making, so `element.src` is
 * no longer the address anybody asked for and every one of those comparisons
 * says "not mine" — the layer becomes unreachable, and the symptom is a climb
 * that silently never completes.
 *
 * So the logical source is written onto the element as `data-source` wherever
 * the `src` is set, and identity is read from there, falling back to `src` for
 * the layers that carry an ordinary file.
 */
function showsSource(el: HTMLVideoElement | null | undefined, url: string): boolean {
  if (!el) return false
  const absolute = new URL(url, window.location.href).href
  const declared = el.dataset.source
  if (declared) return new URL(declared, window.location.href).href === absolute
  return el.src === absolute
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
/**
 * Keeps hls.js attached to one `<video>` layer for as long as it is showing a
 * playlist that needs it.
 *
 * Written as a hook so the two layers get one implementation rather than two
 * that must agree. The import inside `attachHLS` is dynamic, so a browser with
 * native HLS never downloads the library at all.
 */
function useAttachedHLS(
  ref: React.RefObject<HTMLVideoElement | null>,
  src: string | undefined,
  height: number | undefined,
) {
  const attachment = useRef<HLSAttachment | undefined>(undefined)

  useEffect(() => {
    const el = ref.current
    if (!el || !src || !needsHLSLibrary(src)) return

    // The attachment is asynchronous — the library has to arrive first — so the
    // source may already have moved on by the time it does. `cancelled` is what
    // stops an attachment nobody wants any more from starting, and the detach
    // below is what stops the one that did start.
    let cancelled = false
    void attachHLS(el, src).then((handle) => {
      if (cancelled) {
        handle.detach()
        return
      }
      attachment.current = handle
      // Apply whatever was already chosen. The viewer may have pinned a height
      // on the previous video, and the menu is read before this resolves.
      handle.selectHeight(heightRef.current)
    })

    return () => {
      cancelled = true
      attachment.current?.detach()
      attachment.current = undefined
    }
  }, [ref, src])

  // Kept in a ref as well, so the attachment above can read the current choice
  // without this effect being torn down and rebuilt every time it changes —
  // which would drop the buffer and restart the video to change a rendition.
  const heightRef = useRef(height)
  heightRef.current = height

  useEffect(() => {
    attachment.current?.selectHeight(height)
  }, [height])
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
  const { t } = useTranslation()
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
  // The rendition the viewer pinned, or undefined for "let the player choose".
  //
  // Only meaningful where a level can actually be selected — through hls.js.
  // Native HLS offers a page no way to pin one, so on iPhone this stays
  // undefined and the menu says so rather than offering a control that cannot
  // act (CLAUDE.md §5).
  const pinnedHeight =
    quality === 'high' && canSelectHLSLevel() ? PINNED_HEIGHT : undefined
  // After the preference, which it now reads: pinning the high rendition
  // changes what the muxed tier asks the server for.
  const tiers = useMemo(() => availableTiers(sources, quality), [sources, quality])
  // Bumped to send the climb round again when nothing else would — an
  // abandoned climb to the local file, which nothing else re-triggers once the
  // stream answer has stopped being polled.
  const [climbAttempt, setClimbAttempt] = useState(0)
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
  // What the hidden layer is loading. Needed because whether the audio graph
  // can carry a layer depends on what that layer is playing, and during a
  // handover the two layers are playing different things.
  const backSrcNow = frontIsA ? srcB : srcA
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
  /**
   * The rewindable window of a broadcast, read from the element itself.
   *
   * A live stream declares no duration at all, so everything the bar is
   * normally drawn against is zero — and `position / max(duration, 1)` on a
   * stream 26 minutes in is 155,700%, which is a bar painted solid red from
   * the first second. It read "25:57 / 0:00" beside it.
   *
   * What a live playlist does declare is `seekable`, and that is the honest
   * thing to draw: measured on two real broadcasts, 0..3605 on one and 0..1285
   * on another. The window is YouTube's, it moves forward as the broadcast
   * runs, and it is the only statement of length that exists here.
   */
  const [liveWindow, setLiveWindow] = useState<{ start: number; end: number } | null>(null)
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
  const pendingTierRef = useRef<{ tier: Tier; url: string } | undefined>(undefined)
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

  // What the element says, whenever it has said anything.
  //
  // This used to be refused for anything that was not the file on disk, and the
  // comment above it argued both sides at once: a *fragmented* stream declares
  // no total length — true of the muxed tier, which no longer exists — while
  // "the element's own duration is trustworthy for HLS, the playlist states
  // it" was written directly underneath and never acted on.
  //
  // So a video the catalogue has no length for read **0:00** for as long as it
  // streamed, and only gained a duration when the local file landed — which for
  // a video nobody downloads is never. Measured on DM2WU9gbNGc: the catalogue
  // says 0 because it arrived through a flat listing, while its playlist is
  // `EXT-X-PLAYLIST-TYPE:VOD` with an `ENDLIST` and 3,166 segments summing to
  // 16,254 seconds. The answer was there the whole time.
  //
  // The catalogue stays the fallback for the moment before metadata arrives,
  // which is what it was always good for.
  //
  // `offsetSeconds` is always zero today: nothing sets it to anything else, and
  // the guard is left standing only because reintroducing an offset without it
  // would draw a half-watched film as barely begun.
  // A broadcast still on air. Asked of the tier rather than of the catalog row,
  // because the tier is what is actually playing: the row can say "live" for up
  // to thirty minutes after a broadcast has ended, and the player must follow
  // the stream in front of it.
  const isLive = tier?.name === 'live'
  const duration = playbackDuration({
    elementDuration,
    catalogueDuration: durationSeconds,
    liveWindow,
    isLive,
    offsetSeconds,
  })
  // Where the bar begins. Always zero except on a broadcast, whose window slides
  // forward and eventually leaves zero behind it.
  const timelineOrigin = isLive ? (liveWindow?.start ?? 0) : 0
  // Whether the viewer is watching what is happening rather than a rewind.
  // The rule lives in live-timeline.ts, with the arithmetic the bar uses, so
  // the label and the bar can never disagree about where the edge is.
  const onLiveEdge = isLive && atLiveEdge(liveWindow, position)

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
  // Whether anything can speak at all. Asked of the server rather than assumed:
  // there is no built-in synthesiser address any more, deliberately, so a fresh
  // install has none until somebody sets one.
  const { data: ttsConfig } = useTTSConfig()
  const ttsReady = Boolean(ttsConfig?.baseUrl)
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
    // Whether the graph is actually carrying each layer's sound, which is not
    // the same as whether it was attached. On iOS an HLS source never reaches
    // Web Audio — measured, through hls.js as much as natively — while
    // `createMediaElementSource` still succeeds, so the attachment looks
    // healthy and the gain node is wired to nothing. Asked per layer, because
    // during a handover the two are playing different things.
    const graphHasFront = isAttached(el) && !bypassesWebAudio(frontSrc)
    const graphHasHidden = isAttached(hidden) && !bypassesWebAudio(backSrcNow)

    if (hidden) {
      if (graphHasHidden) setElementGain(hidden, 0)
      else hidden.volume = 0
    }
    if (!el) return
    el.muted = muted
    // The fallback is not decoration, and it now covers two cases rather than
    // one. A browser with no Web Audio — an older television, which is where
    // this is headed — attaches nothing. And a phone streaming HLS attaches
    // perfectly well and carries no signal, which left the volume slider inert
    // on every iPhone for the seconds before a download landed: a dead control,
    // unnoticed only because a phone has buttons of its own.
    if (graphHasFront) setElementGain(el, levels.video)
    else el.volume = levels.video
    // `playable` is in here because it is what puts the two layers into the tree.
    // A freshly attached element's gain starts at zero — so that a hidden layer
    // is never heard on the way in — and if this did not run again after that,
    // the fresh element would be the one in front and permanently silent.
  }, [levels.video, muted, front, back, frontSrc, backSrcNow, frontIsA, playable])

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
    log('cue source', {
      videoId,
      tracks: subtitles.map((s) => s.language).join(',') || '(none)',
      settled: captionsSettled(subtitles),
    })
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
    // Keyed on the addresses, not on the array.
    //
    // `subtitleKey` exists for this and nothing used it: the array arrives from
    // a query, so every refetch — and the player polls while a video downloads
    // — hands over a new reference with identical contents. Depending on it ran
    // this effect on data that had not changed, and the call below is not free
    // to repeat.
    //
    // `videoId` is in here because `resetNarration` clears the cue list on it,
    // and nothing else brings them back. Two pieces of the same state reset by
    // two different keys is how the pass ended up waiting on a fetch nobody had
    // started — see whenCuesReady. Repeating the call is free: the same address
    // is a no-op.
  }, [narrationOn, captions, subtitleKey, videoId])

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
    if (!captionsSettled(subtitles) || hasVi) {
      log('translation not asked for', {
        videoId,
        settled: captionsSettled(subtitles),
        hasVi,
        tracks: subtitles.length,
      })
      return
    }
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
    setLiveWindow(null)
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
      'quality', quality, tier.url)
  }, [tier, videoId, quality])

  // Attach hls.js to whichever layer is carrying a playlist it cannot play alone.
  //
  // One effect per layer rather than one for both, so a climb — which loads the
  // new source into the *hidden* layer while the front one keeps playing —
  // tears down and rebuilds only the side that changed.
  //
  // Keyed on the URL: a new source means a new attachment, and the previous one
  // has to be destroyed or its segment fetches carry on against an element that
  // has moved to something else. The teardown also runs on unmount, which is
  // what stops a page-away from leaving a worker fetching video.
  //
  // Nothing happens here on Safari or iOS. There the playlist is an ordinary
  // `src` and the browser does all of this itself.
  //
  // The height is what the quality menu chose, or undefined for automatic.
  // Changing it moves the ladder without reloading anything: hls.js switches
  // rendition at the next segment boundary.
  useAttachedHLS(videoARef, srcA, pinnedHeight)
  useAttachedHLS(videoBRef, srcB, pinnedHeight)

  // What this browser says it can play, said once.
  //
  // Every wrong turn in this area came from believing a capability check
  // instead of an outcome: `canPlayType` answers "maybe" both on the browser
  // that plays HLS and on the one that fails it with MEDIA_ERR_SRC_NOT_SUPPORTED.
  // Printing the claim beside the decision is what makes the next disagreement
  // visible from a device with no console worth the name.
  useEffect(() => {
    const caps = hlsCapabilities()
    console.info('[debug] hls', 'native', caps.native, 'withLibrary', caps.withLibrary,
      'canPlayType says', JSON.stringify(caps.claim))
  }, [])

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
    setLocalAttempts(0)
  }, [loadFailed, sources?.local?.url])

  // Load the opening source, and afterwards prepare any better one out of sight.
  //
  // This is the whole of the tier machinery: the front element is never asked
  // to change what it is playing, because that is precisely what makes the
  // picture drop out. A replacement is loaded into the hidden element instead,
  // and the two are exchanged once the replacement is genuinely ready.
  useEffect(() => {
    // Nothing playing yet: open the best source straight into the front
    // element. There is no picture to protect.
    //
    // No mark and no offset. Every tier can seek, so a video resumed part way
    // through is opened at its own start and moved to the saved position by the
    // element itself, in `onLoadedMetadata`. That used to be impossible: the
    // muxed stream had to be *opened* at the mark, which meant asking the
    // server where the mark really landed, carrying the difference as an offset
    // through every position in the player, and refining it a round trip later.
    if (!frontSrc) {
      const opening = openingTier(tiers)
      if (!opening) return
      setTier(opening)
      if (frontIsARef.current) setSrcA(opening.url)
      else setSrcB(opening.url)
      return
    }

    const wanted = targetTier(tiers, tier?.name, localFailed)
    if (!wanted) return

    // One attempt per tier at a time. Keyed on the tier rather than the URL,
    // which mattered when a URL carried a mark that moved with the playhead;
    // it is kept because the identity being asserted is "a climb to this tier
    // is already under way", which is what the guard means.
    if (pendingTierRef.current?.tier.name === wanted.name) return
    if (upgradingToRef.current === wanted.url) return

    upgradingToRef.current = wanted.url
    pendingTierRef.current = { tier: wanted, url: wanted.url }
    setBackSrc(wanted.url)
  }, [tiers, tier, localFailed, climbAttempt, frontSrc, setBackSrc])

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
  // Hand over to the prepared element once it can actually play.
  //
  // What this used to be, and why it is now nine lines: the muxed stream had no
  // index, so the replacement could not be moved to the viewer — it had to be
  // *opened* at a mark guessed far enough ahead that preparation would finish
  // before playback arrived there. That guess needed a measured lead, a
  // keyframe probe to learn where the stream really began, an offset carried
  // through every position in the player, a frame loop watching for the
  // playhead to reach the mark, a tolerance for arriving late, a budget of
  // reopens for arriving too late, and separate rules for a playhead that was
  // not moving at all. Every one of those was added to fix a real fault, and
  // every one of them existed because of a missing index.
  //
  // Both sources seek now. So: put the replacement where the viewer is, wait
  // until it has a frame there, and exchange.
  const handoverToBack = useCallback(() => {
    const current = front()
    const next = back()
    if (!current || !next) return

    // Refuse a claim that is not about this element. Kept from the old
    // machinery, and still earned: the local file landing writes a new claim
    // while the previous one is still loading into this same element, and
    // swapping on the wrong one puts the picture and the tier permanently out
    // of step.
    const claimMatches = () => {
      const pending = pendingTierRef.current
      if (!pending) return false
      return showsSource(next, pending.url)
    }
    if (!claimMatches()) return

    const commit = () => {
      if (!claimMatches()) return

      // Carry across everything the viewer set, or the swap would silently undo
      // their mute and their subtitles.
      //
      // Volume is not among them: it belongs to the audio graph, not to the
      // element, and the effect that owns it re-runs on the swap and gives the
      // new front layer its gain.
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

      // Freeze position tracking across the exchange, for the same reason a
      // source change freezes it: the element being left behind reports times
      // that no longer mean anything.
      swappingRef.current = true

      // Take the incoming element's length with it. A handover changes no
      // `src`, so the element arriving at the front never fires
      // `loadedmetadata` again and would keep whatever the last tier reported.
      setElementDuration(Number.isFinite(next.duration) ? next.duration : 0)
      resumeAtRef.current = next.currentTime
      positionRef.current = next.currentTime

      const pending = pendingTierRef.current
      if (pending) setTier(pending.tier)
      pendingTierRef.current = undefined

      const wasPlaying = !current.paused
      justSwappedRef.current = true
      frontIsARef.current = !frontIsARef.current
      setFrontIsA(frontIsARef.current)
      upgradingToRef.current = undefined

      if (wasPlaying) void next.play().catch(() => undefined)
      current.pause()
      // Said outright rather than waited for: both calls above fire events in
      // an order nothing here controls, and the answer is already known.
      setPlaying(!next.paused)

      // Carry the floating window across before the old source is dropped —
      // dropping the source of the element in picture-in-picture closes it.
      if (document.pictureInPictureElement === current) {
        void next.requestPictureInPicture?.().catch(() => undefined)
      }
      if (frontIsARef.current) setSrcB(undefined)
      else setSrcA(undefined)
      swappingRef.current = false
    }

    // Where the viewer is, now. No lead: the replacement is not being opened at
    // a mark that has to be guessed ahead of time, it is being moved to a
    // position it already contains.
    if (seekElement(next, undefined, current.currentTime) !== 'seeked') {
      abandonUpgrade()
      return
    }

    // Wait for a frame at that position before showing it, or the exchange
    // lands on an element that has nothing to draw and the picture blinks.
    const deadline = Date.now() + HANDOVER_PATIENCE_MS
    const waitForData = () => {
      if (!claimMatches()) return
      // HAVE_CURRENT_DATA: there is something to show at the playhead.
      if (next.readyState >= 2) {
        commit()
        return
      }
      if (Date.now() > deadline) {
        // Not a failure of the tier — the source is fine and the viewer is
        // still watching the other one. Give the layer back and let the next
        // poll decide whether to try again.
        abandonUpgrade()
        return
      }
      handoverFrameRef.current = window.requestAnimationFrame(waitForData)
    }
    waitForData()
  }, [front, back, captions, abandonUpgrade])


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
      label={t('ui.autoplay')}
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
        label={t('ui.subtitles')}
        value={captions ?? 'off'}
        onSelect={(v: string) => setCaptions(v === 'off' ? null : v)}
        tall={coarse}
        options={[{ value: 'off', label: t('ui.off') }, ...captionOptions]}
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
      {/* Named for the one thing it does. t('player.readAloud') said nothing about which
          language came out, and this reads the Vietnamese translation and
          nothing else — so a viewer could reasonably have expected it to speak
          the English they were already watching. Switching it on also brings
          the translation into being, which is a great deal to hide behind two
          words that do not mention Vietnamese at all. */}
      {/* Off, and saying why, when no synthesiser has been configured.
          
          A switch that turns on and produces silence is the dead control §5
          forbids, and the worst kind: somebody meeting it goes looking at the
          volume, the subtitles and the video before they think of an empty text
          field on a settings page. The hint names where to go. */}
      <SettingRow
        label={t('player.vietnameseNarration')}
        on={narrationSpeaks}
        onToggle={ttsReady ? toggleSpeak : undefined}
        hint={ttsReady ? undefined : t('narration.notConfigured')}
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

      // A seek is now just a seek.
      //
      // What used to be here: a muxed stream has no index, so moving within it
      // was impossible and the only way to reach a mark was to open a *new*
      // stream there — detouring through the low rendition to keep a picture on
      // screen, claiming the hidden layer, asking the server where the mark
      // really landed, and handing over when it arrived. About fifty lines, and
      // the reason `seeking` existed as a state at all.
      // No tier is passed: every source the player can be on has an index, so
      // there is nothing left for the guard to refuse. It stays in place — and
      // `player-seek.guard.test.ts` keeps it the only door — because the muxed
      // route is still there on the server, and a tier that cannot seek must
      // never again be seekable by accident.
      seekElement(element, undefined, target)
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
  const tierLabel = labelForTier(tier, remuxLabelHeight)

  // Only the choices this video can actually honour. A menu entry that cannot
  // be delivered is worse than one that is missing.
  const qualityOptions = useMemo(() => {
    const options: { value: QualityChoice; label: string }[] = [{ value: 'auto', label: t('ui.auto') }]
    // Offered only where pressing it does something.
    //
    // This was a dead button and it is worth saying how: the height was carried
    // as a label on the tier, while the URL it pointed at was the same master
    // playlist either way. Pressing 1080p relabelled a 720p picture. The
    // playlist now describes a real ladder and hls.js can be told which rung —
    // but only hls.js can. Native HLS gives a page no way to pin a level, so on
    // Safari and iOS the honest menu is Auto alone, and on a LAN that costs
    // nothing: the bandwidth estimate lands on the top rung anyway.
    //
    // "Low" is gone with the progressive rendition it named.
    const streamed = tiers.find((t) => t.name === 'hls')
    if (streamed && canSelectHLSLevel()) {
      options.push({ value: 'high', label: `${PINNED_HEIGHT}p` })
    }
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
        label={t('ui.resolution')}
        value={quality}
        tall={coarse}
        onSelect={(next: QualityChoice) => {
          // Choosing again is a fresh decision, so the attempt count starts
          // over: someone asking for 1080p after auto gave up should get a try,
          // not the memory of the last failure.
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
          <span title={t('player.streaming')}>
            {tierLabel}
          </span>
          {queuedBehind && (
            <>
              <span className="h-3 w-px bg-white/25" />
              <span>{t('player.copyQueued')}</span>
            </>
          )}
          {transferring && (
            <>
              <span className="h-3 w-px bg-white/25" />
              <span className="flex items-center gap-1.5">
                <span
                  className="h-1 w-16 overflow-hidden rounded-full bg-white/25"
                  role="progressbar"
                  aria-label={t('player.downloadProgress')}
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
          <span className="rounded-lg bg-badge px-3 py-2 text-sm font-medium">{t('ui.seeking')}</span>
        </div>
      )}

      {countdown !== null && (
        <div className="absolute inset-0 z-20 grid place-items-center bg-black/75 px-6 text-center">
          <div>
            <p className="text-sm text-text-2">{t('more.upNextInSeconds', { seconds: countdown })}</p>
            {nextVideoTitle && <p className="mt-1 clamp-2 text-base font-medium">{nextVideoTitle}</p>}
            <button
              type="button"
              onClick={() => {
                setCountdown(null)
                resetAutoplayChain()
              }}
              className="mt-4 rounded-full bg-surface px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
            >
              {t('common.cancel')}
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
                // Withheld where hls.js has to attach the source itself.
                // Assigning a playlist to `src` on a browser with no native HLS
                // is an immediate MEDIA_ERR_SRC_NOT_SUPPORTED, which would
                // count as a failed tier before the library had a chance.
                //
                // Safari and iOS take the playlist as an ordinary `src` and are
                // left exactly as they are — that is the path measured working
                // on the device with nothing behind it.
                src={needsHLSLibrary(src) ? undefined : src}
                // What this layer is *meant* to be playing, which is not always
                // what `src` says: hls.js replaces it with a blob of its own.
                // Every identity check in the tier machinery reads this.
                data-source={src}
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
                    // The replacement has told us how long it is; everything
                    // else about placing it belongs to the handover, which
                    // moves it to the viewer's position and waits for a frame.
                    //
                    // This branch used to do that work here, because a muxed
                    // stream could not be moved: it had to be judged by the
                    // mark it was opened at, in a coordinate frame of its own,
                    // against a playhead that had since moved on — a lead, an
                    // offset, a tolerance and four outcomes. All of it went
                    // when the sources gained an index.
                    handoverToBack()
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
                    seekElement(element, undefined, resumeAt)
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
                        // A broadcast's window moves as it runs, so this is
                        // read continuously rather than once at metadata. Only
                        // for live: on every other tier the bar is drawn
                        // against a length that is already known and does not
                        // change, and seekable there would only be a second
                        // answer to a settled question.
                        if (isLive) {
                          const seekable = e.currentTarget.seekable
                          if (seekable.length > 0) {
                            setLiveWindow({
                              start: seekable.start(0),
                              end: seekable.end(seekable.length - 1),
                            })
                          }
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
                    if (claim && !showsSource(e.currentTarget, claim.url)) {
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
                  // There is nothing below the streamed tier to retreat to any
                  // more. It used to be the progressive rendition, and before
                  // that a lower mux; both are gone, and what replaced them is
                  // the download — which is already running and already has a
                  // progress bar. So a source that will not load is re-resolved
                  // once and then reported, and the effect on the local URL
                  // unlocks the player when the file lands.
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
            ? t('player.findingStream')
            : loadFailed
              ? // A failed stream is not a failed video. Before the copy lands
                // the muxed stream is the only source, and upstream refuses one
                // often enough that a dead end here would be the ordinary
                // outcome — while the download beside it carries on and
                // finishes, usually within seconds. So this says what is
                // actually happening, and the player starts on its own the
                // moment the file is there (see the effect on the local URL).
                transferring
                ? t('player.streamFailedDownloading', { percent: downloadPercent })
                : queuedBehind
                  ? t('player.streamFailedQueued')
                  : t('player.streamFailed')
              : sources?.upcoming
                ? // Nothing is wrong and nothing is missing: YouTube publishes
                  // nothing for a stream until it begins. Said plainly, because
                  // the alternative the viewer met was a generic failure over a
                  // video that would have worked by simply waiting.
                  //
                  // No retry button. The player already polls this answer, so
                  // the broadcast starting is picked up without being asked.
                  t('player.notStartedYet')
                : unavailableReason
                ? unavailableCopy(unavailableReason, t)
                : mediaState === 'EVICTED'
                ? t('player.evicted')
                : sources?.streamError
                  ? // A code from the gateway, mapped here. The server does not
                    // know what language the viewer reads, and a sentence
                    // written there would arrive in English on a Vietnamese
                    // screen.
                    serverCopy(sources.streamError, t)
                  : streamFailed
                    ? t('player.nothingPlayable')
                    : t('player.noFile')}
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
          origin={timelineOrigin}
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
            aria-label={playing ? t('ui.pause') : t('ui.play')}
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
              aria-label={t('player.nextVideo')}
              title={
                nextVideoTitle
                  ? t('upNext.nextIn', { title: nextVideoTitle })
                  : t('player.nextVideo')
              }
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
              {isLive ? (
                // A broadcast has no total, so "25:57 / 0:00" was two wrong
                // numbers rather than one. What a viewer actually wants to know
                // here is whether they are watching what is happening, and if
                // not, how far back — so that is what it says.
                //
                // The button is the way forward again. Pressing it is the only
                // way back to the edge once you have rewound, short of
                // reloading, and it doubles as the label when you are already
                // there: lit is "you are live", dimmed is "you are not, press
                // to be".
                <button
                  type="button"
                  onClick={() => seekTo(duration)}
                  disabled={onLiveEdge}
                  aria-label={onLiveEdge ? t('chips.live') : t('player.goToLive')}
                  className="flex items-center gap-1.5 rounded px-1 text-white transition-opacity duration-150 ease-out disabled:cursor-default"
                >
                  <span
                    className={clsx(
                      'h-2 w-2 rounded-full',
                      onLiveEdge ? 'bg-brand' : 'bg-white/40',
                    )}
                  />
                  {onLiveEdge
                    ? 'LIVE'
                    : `LIVE · −${formatDuration(Math.max(duration - position, 0))}`}
                </button>
              ) : (
                <>
                  {formatDuration(position)} / {formatDuration(duration)}
                </>
              )}
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
              label={t('ui.audio')}
              wide
            >
              <EqualizerSetting
                audio={audio}
                onChange={setAudio}
                element={front()}
                source={frontSrc}
              />
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
              aria-label={t('player.pictureInPicture')}
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
              aria-label={t('player.fullScreen')}
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
            aria-label={t('player.expand')}
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
            aria-label={t('player.closePlayer')}
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
            aria-label={t('player.expand')}
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
            aria-label={t('player.expand')}
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
              aria-label={playing ? t('ui.pause') : t('ui.play')}
            >
              {playing ? <Pause size={20} /> : <Play size={20} />}
            </button>
            <button
              type="button"
              onClick={onClose}
              className="grid h-10 w-10 place-items-center rounded-full text-white"
              aria-label={t('player.closePlayer')}
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
  label,
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
  const { t } = useTranslation()
  // Resolved here, not as a default in the signature: a default is evaluated
  // where the parameters are, which is outside the component body and so
  // outside anywhere a hook may be called.
  const menuLabel = label ?? t('ui.settings')
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
        aria-label={menuLabel}
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
  const { t } = useTranslation()
  // Every phase gets its own words, for the reason recorded on the translation
  // status below: several distinct states behind one hopeful label is how
  // "stuck on preparing" gets reported. In particular a sweep waiting on the
  // translator and a sweep waiting out a dead synthesiser look identical from
  // the outside and want completely different responses from the viewer.
  const label: Record<typeof p.phase, string> = {
    // Every label names its subject. The row above reports translation and this
    // one reports speech, and a bare "Not started" on both left two identical
    // lines stacked on each other with nothing to say which was which.
    idle: t('player.speech.notStarted'),
    sweeping: t('player.speech.preparing'),
    'awaiting-translation': t('player.speech.waitingTranslation'),
    'backing-off': t('player.speech.unavailable'),
    done: t('player.speech.ready'),
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
            {t('more.linesProgress', { done: p.done, total: p.total })}
          </span>
        )}
      </div>
      {p.etaSeconds !== null && (
        <div className="pb-1 text-xs text-text-2">
          {t('more.etaLeft', { eta: formatEta(p.etaSeconds) })}
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
          {t('more.tooFastLines', { count: p.tooFast })}
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
  const { t } = useTranslation()
  // "Preparing" was true of a pass that had not started, one waiting on a
  // subtitle file, one whose subtitles never arrived, and one with nothing to
  // do because the cues were already Vietnamese. Four states behind one word is
  // no better than no status at all — it was reported as "stuck on preparing".
  const label: Record<typeof p.phase, string> = {
    idle: t('player.translation.notStarted'),
    // Each step before the first batch says which step it is. One word over all
    // of them meant a pass held up by the translator settings — or by hashing a
    // long video's cues — claimed to be loading subtitles that were already on
    // screen, and there was no way to tell which from the outside.
    'waiting-config': t('player.translation.waitingSettings'),
    'no-translator': t('player.translation.noModel'),
    'reading-cache': t('player.translation.readingSaved'),
    'waiting-subtitles': t('player.translation.loadingSubtitles'),
    hashing: t('player.translation.preparingCues'),
    'no-subtitles': t('player.translation.noSubtitles'),
    'not-needed': t('player.translation.alreadyVietnamese'),
    translating: t('ui.translating'),
    done: t('ui.translated'),
    failed: p.error
      ? t('player.translation.failedWith', { error: p.error })
      : t('player.translation.failed'),
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
            {t('more.linesProgress', { done: p.done, total: p.total })}
          </span>
        )}
      </div>
      {p.etaSeconds !== null && (
        <div className="pb-1 text-xs text-text-2">
          {t('more.etaLeft', { eta: formatEta(p.etaSeconds) })}
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
  hint,
}: {
  label: string
  on: boolean
  /** Absent where the setting cannot be changed yet — see `hint`. */
  onToggle?: () => void
  /**
   * Why this row cannot be used.
   *
   * A switch with no handler and nothing to say is the dead control §5
   * forbids. Present, disabled, and explaining itself is a different thing:
   * it says the feature exists and what stands between you and it.
   */
  hint?: string
}) {
  const disabled = !onToggle
  return (
    <li>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-disabled={disabled}
        disabled={disabled}
        onClick={onToggle}
        // No hover fill. The switch beside the label is the feedback — it slides
        // and changes colour on press — and a row that lit up under the pointer
        // as well said the same thing twice. `transition-colors` goes with it:
        // nothing on this row changes colour any more.
        className={clsx(
          'flex w-full items-center justify-between gap-4 px-4 py-3 text-left',
          disabled && 'opacity-50',
        )}
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
      {/* Outside the button on purpose. Inside it, the explanation became part
          of the switch's accessible name — "Vietnamese narration No speech
          service set…" — which is what a screen reader would then announce as
          the control's name. */}
      {hint && <p className="px-4 pb-2 text-xs text-text-2">{hint}</p>}
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
  const { t } = useTranslation()
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
        aria-label={t('ui.subtitles')}
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
              {t('ui.off')}
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
  origin = 0,
  buffered,
  disabled,
  onScrub,
  onSeek,
}: {
  position: number
  duration: number
  /**
   * Where the timeline starts. Zero for anything with a beginning.
   *
   * A broadcast has none: its rewindable window slides forward as it runs, so
   * the earliest thing that can still be played is not second zero. Drawing it
   * from zero would give a bar whose filled part shrinks while the picture
   * plays forward.
   */
  origin?: number
  buffered: number
  disabled: boolean
  /** Called continuously while dragging. Moves the readout, nothing else. */
  onScrub: (next: number) => void
  /** Called once, when the handle is released or a key press lands. */
  onSeek: (next: number) => void
}) {
  const { t } = useTranslation()
  const safeDuration = Math.max(duration, origin + 1)
  // Everything is measured from the origin, and clamped.
  //
  // The clamp is not defensive tidiness: a live stream reports no duration at
  // all, so before the window is known this arithmetic is position over one —
  // 155,700% on a broadcast 26 minutes in, drawn as a bar solid red from the
  // first second, with "25:57 / 0:00" beside it.
  // The same arithmetic the live readout uses, from the same function. Written
  // twice they would agree until one of them was fixed.
  const window = { start: origin, end: safeDuration }
  const playedPercent = livePercent(window, position)
  const bufferedPercent = livePercent(window, buffered)

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
        min={origin}
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
        aria-label={t('ui.seek')}
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
  const { t } = useTranslation()
  const Icon = muted || volume === 0 ? VolumeX : volume < 0.5 ? Volume1 : Volume2

  return (
    <div className="group/volume flex items-center">
      <button
        type="button"
        aria-label={muted ? t('ui.unmute') : t('ui.mute')}
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
        aria-label={t('ui.volume')}
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
/**
 * A code the gateway sent, in the viewer's language.
 *
 * Unknown codes fall through to the generic stream failure rather than being
 * printed raw: an older gateway sending prose would otherwise put an English
 * sentence on screen, which is the thing this exists to stop.
 */
export function serverCopy(code: string, t: TFunction): string {
  switch (code) {
    case 'media_root_unavailable':
      return t('common.mediaRootUnavailable')
    default:
      return t('player.streamFailed')
  }
}

export function unavailableCopy(reason: UnavailableReason, t: TFunction): string {
  switch (reason) {
    case 'members_only':
      return t('player.unavailable.membersOnly')
    case 'private':
      return t('player.unavailable.private')
    case 'removed':
      return t('player.unavailable.removed')
    default:
      return t('player.unavailable.generic')
  }
}
