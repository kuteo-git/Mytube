import { describe, expect, it } from 'vitest'

import {
  buildImpulseResponse,
  clampWet,
  DEFAULT_WET,
  MAX_REVERB_SECONDS,
  normaliseReverb,
  REVERB_PRESETS,
  reverbPresetByName,
} from './reverb-presets'

/** Just enough of BaseAudioContext for `buildImpulseResponse`. */
function fakeContext(sampleRate = 48000): BaseAudioContext {
  return {
    sampleRate,
    createBuffer(channels: number, length: number, rate: number) {
      const data = Array.from({ length: channels }, () => new Float32Array(length))
      return {
        numberOfChannels: channels,
        length,
        sampleRate: rate,
        duration: length / rate,
        getChannelData: (i: number) => data[i],
      }
    },
  } as unknown as BaseAudioContext
}

describe('presets', () => {
  it('stays inside the length the television has to convolve', () => {
    // Convolution scales with the tail, and this is headed for a TV browser.
    for (const preset of REVERB_PRESETS) {
      expect(preset.seconds).toBeLessThanOrEqual(MAX_REVERB_SECONDS)
    }
  })

  it('gives each room a distinct character rather than four lengths', () => {
    // The reason there are four presets and not Apple's dozen: each one has to
    // be tellable from the others by ear, or it is a label over a sound that
    // already has one.
    const damping = REVERB_PRESETS.map((p) => p.damping)
    expect(new Set(damping).size).toBe(REVERB_PRESETS.length)
  })

  it('grows darker as the rooms grow larger', () => {
    const room = reverbPresetByName('room')!
    const cathedral = reverbPresetByName('cathedral')!
    expect(cathedral.seconds).toBeGreaterThan(room.seconds)
    expect(cathedral.damping).toBeGreaterThan(room.damping)
    expect(cathedral.preDelay).toBeGreaterThan(room.preDelay)
  })

  it('does not know a room that was never built', () => {
    expect(reverbPresetByName('largeHall2')).toBeUndefined()
  })
})

describe('the wet mix', () => {
  it('holds between silence and all room', () => {
    expect(clampWet(2)).toBe(1)
    expect(clampWet(-1)).toBe(0)
    expect(clampWet(0.3)).toBe(0.3)
  })

  it('falls back rather than passing nonsense to a gain node', () => {
    expect(clampWet(Number.NaN)).toBe(DEFAULT_WET)
  })

  it('starts at a quarter, not half', () => {
    // Reverb is the one control here that can make music worse rather than
    // different, and something that arrives drenched gets switched off.
    expect(DEFAULT_WET).toBeLessThanOrEqual(0.25)
  })
})

describe('normaliseReverb', () => {
  it('comes back off when there is nothing stored', () => {
    expect(normaliseReverb(null).enabled).toBe(false)
  })

  it('keeps a stored room', () => {
    const out = normaliseReverb({ enabled: true, preset: 'hall', wet: 0.4 })
    expect(out).toEqual({ enabled: true, preset: 'hall', wet: 0.4 })
  })

  it('falls back to a real room when the stored name is gone', () => {
    const out = normaliseReverb({ enabled: true, preset: 'cavern' as never, wet: 0.5 })
    expect(reverbPresetByName(out.preset)).toBeDefined()
  })

  it('brings an out-of-range mix back in', () => {
    expect(normaliseReverb({ enabled: true, preset: 'room', wet: 9 }).wet).toBe(1)
  })
})

describe('buildImpulseResponse', () => {
  it('is as long as the preset says', () => {
    const ctx = fakeContext(48000)
    const preset = reverbPresetByName('room')!
    const ir = buildImpulseResponse(ctx, preset)
    expect(ir.length).toBe(Math.floor(48000 * preset.seconds))
    expect(ir.numberOfChannels).toBe(2)
  })

  it('never exceeds the cap, whatever it is asked for', () => {
    const ctx = fakeContext(48000)
    const ir = buildImpulseResponse(ctx, {
      name: 'hall',
      label: 'Hall',
      seconds: 30,
      decay: 2,
      preDelay: 0,
      damping: 0.5,
    })
    expect(ir.length).toBeLessThanOrEqual(Math.floor(48000 * MAX_REVERB_SECONDS))
  })

  it('leaves the pre-delay silent', () => {
    const ctx = fakeContext(48000)
    const preset = reverbPresetByName('cathedral')!
    const ir = buildImpulseResponse(ctx, preset)
    const preDelay = Math.floor(48000 * preset.preDelay)
    const head = ir.getChannelData(0).slice(0, preDelay)
    expect(head.every((s) => s === 0)).toBe(true)
    expect(preDelay).toBeGreaterThan(0)
  })

  it('decays rather than running at level to the end', () => {
    const ctx = fakeContext(48000)
    const ir = buildImpulseResponse(ctx, reverbPresetByName('hall')!)
    const data = ir.getChannelData(0)
    const peak = (from: number, to: number) => {
      let max = 0
      for (let i = from; i < to; i++) max = Math.max(max, Math.abs(data[i]))
      return max
    }
    const early = peak(Math.floor(data.length * 0.1), Math.floor(data.length * 0.2))
    const late = peak(Math.floor(data.length * 0.8), Math.floor(data.length * 0.9))
    expect(late).toBeLessThan(early)
  })

  it('gives the two channels different noise', () => {
    // Identical channels convolve to a tail dead in the centre of the image,
    // which sounds like a fault rather than like a room.
    const ctx = fakeContext(48000)
    const ir = buildImpulseResponse(ctx, reverbPresetByName('plate')!)
    const left = ir.getChannelData(0)
    const right = ir.getChannelData(1)
    const identical = left.every((s, i) => s === right[i])
    expect(identical).toBe(false)
  })

  it('produces a finite signal everywhere, including the sample at the very end', () => {
    // The last sample divides by the tail length; an off-by-one there yields
    // NaN, and a NaN in an impulse response silences the convolver outright.
    const ctx = fakeContext(8000)
    for (const preset of REVERB_PRESETS) {
      const ir = buildImpulseResponse(ctx, preset)
      const data = ir.getChannelData(0)
      expect(Number.isFinite(data[data.length - 1])).toBe(true)
    }
  })
})
