/**
 * The one audio graph, and the one `AudioContext` under it.
 *
 * Two features need Web Audio now — narration, which places synthesised speech
 * on a timeline, and the equaliser, which shapes what the video plays — and a
 * page may have exactly one context worth having. This module owns it, so
 * neither feature has to know the other exists, and above all so the rule that
 * governs the context is written once:
 *
 *   graph
 *     video A ─▶ gainA ─┐
 *                       ├─▶ eqInput ─▶ [10 biquads] ─▶ preamp ─▶ destination
 *     video B ─▶ gainB ─┘
 *
 *     narration masterGain ─▶ limiter ─▶ destination
 *
 * Narration deliberately does not pass through the filters. The equaliser is for
 * shaping music; a voice reading subtitles aloud through a bass boost is a fault,
 * not a feature, and its own limiter (`narration.ts connectOutput`) is already
 * the right treatment for it.
 *
 * ## Why the resume logic is so insistent
 *
 * A media element routed into Web Audio stays routed: `createMediaElementSource`
 * may be called once per element and there is no call that undoes it. From that
 * moment the element's sound reaches the speakers only through this graph — so a
 * context that is `suspended` is not a degraded equaliser, it is a player with no
 * sound at all, for a viewer who may never have opened the equaliser. A context
 * built without a gesture is born suspended, and iOS suspends one again after a
 * phone call or a change of audio route.
 *
 * Hence: the gesture listeners are installed unconditionally the moment the
 * context exists rather than when a feature wants it, they re-arm whenever the
 * state leaves `running`, and `resumeAudio` is called from the player on play and
 * on the page becoming visible. Three chances at the same thing, because the
 * failure is total and cannot be backed out of.
 */

import {
  BANDS,
  BAND_Q,
  dbToGain,
  type EqSettings,
} from './eq-presets'
import {
  buildImpulseResponse,
  clampWet,
  reverbPresetByName,
  type ReverbPreset,
  type ReverbPresetName,
  type ReverbSettings,
} from './reverb-presets'

interface Graph {
  ctx: AudioContext
  /** Where every element's gain lands, ahead of the filters. */
  eqInput: GainNode
  filters: BiquadFilterNode[]
  /** The signal as it left the equaliser, untouched by the room. */
  dry: GainNode
  convolver: ConvolverNode
  /** The room's output, and the whole of the Dry/Wet control. */
  wet: GainNode
  preamp: GainNode
}

let graph: Graph | null = null

/**
 * Set once the environment has been found to have no Web Audio at all.
 *
 * Separate from `graph` being null, which only means "not built yet". Without
 * it every call would try the constructor again — and there is a caller on a
 * 100 ms timer.
 */
let unavailable = false

/**
 * One gain per element, so the two layers can differ.
 *
 * The player keeps a second `<video>` loading and playing out of sight while it
 * prepares a handover, and the filter chain is shared — so without a gain of its
 * own the hidden layer would be summed into the same output and heard underneath
 * the visible one. A `WeakMap` because the key is the element itself and nothing
 * here should keep one alive.
 */
const elementGains = new WeakMap<HTMLMediaElement, GainNode>()

/** Elements already routed, so the once-per-element rule is kept by the module. */
const attached = new WeakSet<HTMLMediaElement>()

function buildGraph(): Graph | null {
  const Ctor: typeof AudioContext | undefined =
    window.AudioContext ??
    (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
  if (typeof Ctor !== 'function') {
    unavailable = true
    return null
  }
  const ctx = new Ctor()

  const eqInput = ctx.createGain()
  eqInput.gain.value = 1

  const filters = BANDS.map((band) => {
    const filter = ctx.createBiquadFilter()
    filter.type = band.kind
    filter.frequency.value = band.frequency
    // Q is meaningless on the two shelves; setting it anyway keeps the array
    // uniform and costs nothing.
    filter.Q.value = BAND_Q
    filter.gain.value = 0
    return filter
  })

  const preamp = ctx.createGain()
  preamp.gain.value = 1

  // The chain exists at full length even with every gain at zero decibels. A
  // biquad at 0 dB is transparent, so "equaliser off" and "equaliser on, flat"
  // are the same signal path — which means turning it on cannot introduce a
  // fault that turning it off hides, and there is no reconnection to perform
  // mid-playback.
  let tail: AudioNode = eqInput
  for (const filter of filters) {
    tail.connect(filter)
    tail = filter
  }

  /*
   * The room splits off here, after the equaliser and before the preamp.
   *
   * After the equaliser because the room should answer the sound the viewer
   * chose, not the one before it. Before the preamp because reverb *adds*
   * energy — a wet signal is the dry one plus a tail — and the preamp is the
   * trim that pays for clipping. Behind it, the one control meant to stop
   * distortion would have no authority over a thing that causes it.
   *
   * Both paths exist permanently, for the reason the filters do: with the wet
   * gain at zero and no impulse response loaded, the convolver emits silence and
   * costs nothing, so "no reverb" is the same graph rather than a different one.
   */
  const dry = ctx.createGain()
  dry.gain.value = 1
  const convolver = ctx.createConvolver()
  // Left at its default `normalize = true`: the synthesised responses are not
  // level-matched to each other, and without it choosing Cathedral would be a
  // change of volume as much as a change of space.
  const wet = ctx.createGain()
  wet.gain.value = 0

  tail.connect(dry)
  tail.connect(convolver)
  convolver.connect(wet)
  dry.connect(preamp)
  wet.connect(preamp)
  preamp.connect(ctx.destination)

  armResume(ctx)
  return { ctx, eqInput, filters, dry, convolver, wet, preamp }
}

/**
 * The shared context, created on first use.
 *
 * Safe to call before any gesture: a suspended context is fine to build and to
 * connect, and `armResume` below is what gets it running.
 *
 * Null where the environment has no Web Audio — an old television browser, or a
 * test running under jsdom. Callers treat that as "no graph", not as an error:
 * the player then plays its video the ordinary way, with no equaliser and no
 * narration, which is a real degradation but an audible one.
 */
export function getAudioContext(): AudioContext | null {
  return getGraph()?.ctx ?? null
}

function getGraph(): Graph | null {
  if (unavailable) return null
  if (!graph) {
    try {
      graph = buildGraph()
    } catch {
      // A constructor that exists but will not build a working graph — an
      // implementation missing a node type, or a context the browser refuses to
      // create because too many are already open. Recorded as unavailable so it
      // is attempted once rather than on every tick, and the player falls back
      // to playing the element directly.
      unavailable = true
      graph = null
    }
  }
  return graph
}

/** The context, only if something already built one. Never creates. */
export function peekAudioContext(): AudioContext | null {
  return graph?.ctx ?? null
}

/**
 * Try to start the context now.
 *
 * Call it from anywhere; it is cheap when already running. Safari only honours a
 * resume from inside the handler for a gesture, which is what `armResume` is for
 * — this is the second chance, taken from `play` and from the tab coming back.
 */
export function resumeAudio(): void {
  const ctx = graph?.ctx
  if (!ctx || ctx.state === 'running') return
  void ctx.resume().catch(() => undefined)
}

/**
 * Listen for the viewer's next gesture and start the context inside it.
 *
 * Detaches once running and re-attaches whenever the state leaves `running`, so
 * an iOS interruption is repaired by the next tap rather than leaving the player
 * mute until a reload. The listeners are on `document` because the gesture that
 * matters is whichever one comes first, and pressing play is only the likeliest.
 */
function armResume(ctx: AudioContext): void {
  let armed = false

  const start = () => {
    if (ctx.state === 'running') {
      disarm()
      return
    }
    void ctx.resume().then(disarm, () => undefined)
  }

  const disarm = () => {
    if (!armed) return
    armed = false
    document.removeEventListener('pointerdown', start)
    document.removeEventListener('keydown', start)
  }

  const arm = () => {
    if (armed) return
    armed = true
    document.addEventListener('pointerdown', start)
    document.addEventListener('keydown', start)
  }

  ctx.addEventListener('statechange', () => {
    if (ctx.state === 'running') disarm()
    else arm()
  })

  if (ctx.state !== 'running') arm()
}

/**
 * Route a media element through the graph, once and for good.
 *
 * Idempotent by necessity rather than by politeness: calling
 * `createMediaElementSource` twice on one element throws, and this is called
 * from an effect in a component that re-renders freely.
 *
 * Returns false when the browser has no usable Web Audio at all, in which case
 * the element is left alone and plays the ordinary way — untouched by the
 * equaliser, but audible, which is the half that matters.
 */
export function attachElement(el: HTMLMediaElement | null | undefined): boolean {
  if (!el) return false
  if (attached.has(el)) return true
  try {
    const g = getGraph()
    if (!g) return false
    const source = g.ctx.createMediaElementSource(el)
    const gain = g.ctx.createGain()
    // Starts silent. The player sets the real value from `levelsFor` on the very
    // next effect, and starting at 1 would let a hidden layer that is already
    // playing be heard for a frame.
    gain.gain.value = 0
    source.connect(gain)
    gain.connect(g.eqInput)
    elementGains.set(el, gain)
    attached.add(el)
    return true
  } catch {
    return false
  }
}

/**
 * How loud this element is, replacing `element.volume`.
 *
 * Volume moved into the graph when the video did. Whether an element's own
 * `volume` still attenuates a signal that has been routed into Web Audio is not
 * something the specification is read the same way about by every browser, and
 * the player cannot afford to find out per device — a gain node is the same
 * arithmetic in a place where the answer is not in doubt.
 *
 * Ramped rather than set: an abrupt change of gain on a signal mid-waveform is a
 * step, and a step is a click. 15 ms is below notice and well short of the
 * ducking that narration performs against speech.
 */
export function setElementGain(el: HTMLMediaElement | null | undefined, value: number): void {
  if (!el) return
  const gain = elementGains.get(el)
  const ctx = graph?.ctx
  if (!gain || !ctx) return
  const target = Number.isFinite(value) ? Math.max(0, value) : 0
  gain.gain.setTargetAtTime(target, ctx.currentTime, 0.015)
}

/** Whether this element's sound is going through the graph. */
export function isAttached(el: HTMLMediaElement | null | undefined): boolean {
  return Boolean(el && attached.has(el))
}

/**
 * Apply the viewer's curve.
 *
 * Disabled means every band flat and the preamp at unity, not a disconnected
 * chain — see `buildGraph` for why the path never changes shape.
 */
export function applyEq(settings: EqSettings): void {
  const g = graph
  if (!g) return
  const now = g.ctx.currentTime
  g.filters.forEach((filter, i) => {
    const db = settings.enabled ? (settings.gains[i] ?? 0) : 0
    filter.gain.setTargetAtTime(db, now, 0.02)
  })
  g.preamp.gain.setTargetAtTime(
    settings.enabled ? dbToGain(settings.preamp) : 1,
    now,
    0.02,
  )
}

/**
 * Impulse responses already built, one per preset.
 *
 * Built on demand rather than at start-up, and kept afterwards. Each one is
 * seconds of stereo audio filled a sample at a time — a couple of megabytes and
 * a loop of a few hundred thousand iterations — which is affordable once when
 * somebody picks a room and not affordable on every change of the wet slider.
 */
const impulses = new Map<ReverbPresetName, AudioBuffer>()

function impulseFor(ctx: BaseAudioContext, preset: ReverbPreset): AudioBuffer {
  const cached = impulses.get(preset.name)
  if (cached) return cached
  const built = buildImpulseResponse(ctx, preset)
  impulses.set(preset.name, built)
  return built
}

/**
 * Apply the room.
 *
 * Dry and wet are a plain crossfade summing to one, which is what "Dry/Wet Mix"
 * means everywhere it appears: at zero the room is silent and the signal is
 * exactly what the equaliser produced.
 *
 * Ramped over 60 ms rather than the 20 ms the equaliser uses. A filter's gain
 * moving quickly is inaudible; a reverb tail appearing quickly is a swell, and
 * the slider is dragged rather than set.
 */
export function applyReverb(settings: ReverbSettings): void {
  const g = graph
  if (!g) return
  const now = g.ctx.currentTime
  const preset = settings.enabled ? reverbPresetByName(settings.preset) : undefined

  if (preset) {
    // Only ever assigned when a room is actually wanted. A convolver holding a
    // buffer convolves whatever reaches it even at zero wet gain, and that is
    // the one cost in this whole graph worth avoiding on a television.
    const buffer = impulseFor(g.ctx, preset)
    if (g.convolver.buffer !== buffer) g.convolver.buffer = buffer
  }

  const wet = preset ? clampWet(settings.wet) : 0
  g.wet.gain.setTargetAtTime(wet, now, 0.06)
  g.dry.gain.setTargetAtTime(1 - wet, now, 0.06)

  if (!preset) {
    // Let the tail finish before the buffer goes, or the room is cut off
    // mid-decay — which is heard as a click rather than as switching something
    // off. Nothing depends on this having happened, so a missed timer is
    // harmless: the wet gain is already zero.
    window.setTimeout(() => {
      if (graph === g && g.wet.gain.value === 0) g.convolver.buffer = null
    }, 400)
  }
}
