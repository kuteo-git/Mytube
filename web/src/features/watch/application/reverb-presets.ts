/**
 * The rooms, as numbers.
 *
 * eqMac's "Environment" is macOS's own `AVAudioUnitReverb` with a preset and a
 * `wetDryMix`, and its preset names — Small Room, Large Hall v2, Cathedral —
 * are Apple's `AVAudioUnitReverbPreset` enumeration verbatim. A browser has no
 * such unit. What it has is `ConvolverNode`, which needs an impulse response:
 * a recording of how a space answers a single click.
 *
 * These are the parameters an impulse response is *synthesised* from, rather
 * than a recording of any real room, so this is an imitation and not a port.
 * That is also why there are four presets here and not Apple's dozen: from
 * decaying noise, "Large Room" and "Large Room v2" would be two labels over one
 * sound, and a control that cannot do what its name says is the dead button the
 * charter forbids — heard rather than seen, which makes it worse and not better.
 */

export type ReverbPresetName = 'room' | 'plate' | 'hall' | 'cathedral'

export interface ReverbPreset {
  name: ReverbPresetName
  label: string
  /** How long the tail runs, in seconds. */
  seconds: number
  /**
   * Shape of the fade. Higher falls away faster at the start and lingers
   * longer at the end, which is what a big space does.
   */
  decay: number
  /** Silence before the first reflection — how far away the walls are. */
  preDelay: number
  /**
   * How quickly the high frequencies are lost, 0–1.
   *
   * The single thing that separates a stone cathedral from a bright plate more
   * than length does: hard small surfaces keep the top end, and air and
   * distance take it away.
   */
  damping: number
}

/**
 * Nothing longer than this, whatever a preset asks for.
 *
 * Convolution is real work and it scales with the tail: this is a library meant
 * to end up in a television's browser, and a four-second impulse response is
 * how a feature nobody switched on makes the video stutter. 2.5s is still
 * plainly a cathedral.
 */
export const MAX_REVERB_SECONDS = 2.5

export const REVERB_PRESETS: readonly ReverbPreset[] = [
  { name: 'room', label: 'Room', seconds: 0.6, decay: 2.2, preDelay: 0.005, damping: 0.35 },
  // Bright and dense and short — a plate is a sheet of metal, not a space, and
  // it is the one setting here that flatters a voice rather than a room.
  { name: 'plate', label: 'Plate', seconds: 1.2, decay: 1.6, preDelay: 0.002, damping: 0.15 },
  { name: 'hall', label: 'Hall', seconds: 2.0, decay: 2.6, preDelay: 0.02, damping: 0.5 },
  {
    name: 'cathedral',
    label: 'Cathedral',
    seconds: MAX_REVERB_SECONDS,
    decay: 3,
    preDelay: 0.04,
    damping: 0.7,
  },
]

export interface ReverbSettings {
  enabled: boolean
  preset: ReverbPresetName
  /** How much of the output is the room, 0–1. */
  wet: number
}

/**
 * Where the mix starts when a preset is chosen.
 *
 * A quarter, not a half. Reverb is the one control here that can make music
 * worse rather than different, and something that arrives drenched is switched
 * off rather than adjusted.
 */
export const DEFAULT_WET = 0.25

export const MAX_WET = 1

export const REVERB_OFF: ReverbSettings = {
  enabled: false,
  preset: 'room',
  wet: DEFAULT_WET,
}

export function clampWet(value: number): number {
  if (!Number.isFinite(value)) return DEFAULT_WET
  return Math.min(MAX_WET, Math.max(0, value))
}

export function reverbPresetByName(name: string): ReverbPreset | undefined {
  return REVERB_PRESETS.find((p) => p.name === name)
}

export function normaliseReverb(
  input: Partial<ReverbSettings> | null | undefined,
): ReverbSettings {
  if (!input) return { ...REVERB_OFF }
  const preset = reverbPresetByName(String(input.preset))
  return {
    enabled: Boolean(input.enabled),
    // An unknown name is a preset from a version that had one. The first room
    // is a defensible answer; refusing to load is not.
    preset: preset ? preset.name : REVERB_OFF.preset,
    wet: clampWet(Number(input.wet ?? DEFAULT_WET)),
  }
}

/**
 * Build the impulse response for a preset.
 *
 * Decaying stereo noise, which is the cheapest thing that convolves into
 * something recognisable as a space. Three details carry all of the character:
 *
 * - **The two channels are independent noise.** Identical channels convolve to a
 *   tail dead in the centre of the image, which sounds like a mistake rather
 *   than like a room.
 * - **Damping is a one-pole lowpass applied along the tail**, so the space grows
 *   darker as it decays rather than being uniformly dull. That is what real
 *   rooms do, and it is most of the difference between these presets.
 * - **The pre-delay is silence at the head**, and it is the whole of how big the
 *   space reads.
 *
 * Called once per preset and cached by the caller: this allocates seconds of
 * audio and runs a loop over every sample of it.
 */
export function buildImpulseResponse(
  ctx: BaseAudioContext,
  preset: ReverbPreset,
): AudioBuffer {
  const rate = ctx.sampleRate
  const seconds = Math.min(MAX_REVERB_SECONDS, preset.seconds)
  const length = Math.max(1, Math.floor(rate * seconds))
  const preDelay = Math.floor(rate * preset.preDelay)
  const buffer = ctx.createBuffer(2, length, rate)

  // A one-pole coefficient: 0 lets everything through, approaching 1 lets
  // almost nothing but the low end through.
  const coefficient = Math.min(0.99, Math.max(0, preset.damping))

  for (let channel = 0; channel < 2; channel++) {
    const data = buffer.getChannelData(channel)
    let last = 0
    for (let i = 0; i < length; i++) {
      if (i < preDelay) {
        data[i] = 0
        continue
      }
      const t = (i - preDelay) / (length - preDelay || 1)
      const noise = Math.random() * 2 - 1
      // The filter's own state carries across samples, so the damping deepens
      // with the tail rather than being a fixed tone.
      last = noise * (1 - coefficient) + last * coefficient
      data[i] = last * Math.pow(1 - t, preset.decay)
    }
  }

  return buffer
}
