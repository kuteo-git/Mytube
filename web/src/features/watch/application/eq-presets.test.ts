import { describe, expect, it } from 'vitest'

import {
  BANDS,
  MAX_BAND_DB,
  clampBand,
  clampPreamp,
  dbToGain,
  identifyPreset,
  normaliseSettings,
  PRESETS,
  settingsForPreset,
} from './eq-presets'

describe('bands', () => {
  it('ends in shelves so the extremes are actually moved', () => {
    // A peaking filter at either end leaves everything beyond it untouched,
    // which is a bass control that does not reach the bass.
    expect(BANDS[0].kind).toBe('lowshelf')
    expect(BANDS[BANDS.length - 1].kind).toBe('highshelf')
    expect(BANDS.slice(1, -1).every((b) => b.kind === 'peaking')).toBe(true)
  })

  it('rises through the audible range in order', () => {
    const frequencies = BANDS.map((b) => b.frequency)
    expect([...frequencies].sort((a, b) => a - b)).toEqual(frequencies)
  })
})

describe('presets', () => {
  it('gives every preset one gain per band', () => {
    for (const preset of PRESETS) {
      expect(preset.gains).toHaveLength(BANDS.length)
    }
  })

  it('pays for its own boosts with attenuation', () => {
    // The reason the preamp exists: a preset that lifts bands without lowering
    // the output clips, and clipping is heard as crackle rather than as volume.
    for (const preset of PRESETS) {
      const boosted = Math.max(0, ...preset.gains)
      if (boosted > 0) expect(preset.preamp).toBeLessThan(0)
    }
  })

  it('keeps every gain inside the range the sliders offer', () => {
    for (const preset of PRESETS) {
      for (const gain of preset.gains) {
        expect(Math.abs(gain)).toBeLessThanOrEqual(MAX_BAND_DB)
      }
    }
  })

  it('leaves Flat flat', () => {
    const flat = settingsForPreset('flat')
    expect(flat.gains.every((g) => g === 0)).toBe(true)
    expect(flat.preamp).toBe(0)
    // Choosing a preset is asking to hear it, so it arrives switched on.
    expect(flat.enabled).toBe(true)
  })
})

describe('identifyPreset', () => {
  it('names the preset when the values still match it', () => {
    expect(identifyPreset(settingsForPreset('bass'))).toBe('bass')
  })

  it('falls to custom once a band is moved', () => {
    const edited = settingsForPreset('bass')
    edited.gains[4] = 3
    expect(identifyPreset(edited)).toBe('custom')
  })

  it('finds its way back when the values are restored', () => {
    // The picker follows the sliders rather than the other way round: dragging
    // a band back onto a preset's shape should say so.
    const edited = settingsForPreset('vocal')
    const original = edited.gains[2]
    edited.gains[2] = 7
    expect(identifyPreset(edited)).toBe('custom')
    edited.gains[2] = original
    expect(identifyPreset(edited)).toBe('vocal')
  })

  it('does not call it a preset when only the preamp differs', () => {
    const edited = settingsForPreset('treble')
    edited.preamp = 0
    expect(identifyPreset(edited)).toBe('custom')
  })
})

describe('clamping', () => {
  it('holds bands inside ±12 dB', () => {
    expect(clampBand(40)).toBe(MAX_BAND_DB)
    expect(clampBand(-40)).toBe(-MAX_BAND_DB)
    expect(clampBand(3)).toBe(3)
  })

  it('never lets the preamp add gain', () => {
    expect(clampPreamp(6)).toBe(0)
    expect(clampPreamp(-40)).toBe(-12)
  })

  it('treats a value that is not a number as no adjustment', () => {
    expect(clampBand(Number.NaN)).toBe(0)
    expect(clampPreamp(Number.POSITIVE_INFINITY)).toBe(0)
  })
})

describe('normaliseSettings', () => {
  it('fills a gains array of the wrong length', () => {
    // The band list may grow. A stored curve from before that must not leave
    // the new filters unset — which would be a curve the viewer never chose.
    const out = normaliseSettings({ enabled: true, gains: [5, 5], preamp: -3, preset: 'custom' })
    expect(out.gains).toHaveLength(BANDS.length)
    expect(out.gains.slice(2).every((g) => g === 0)).toBe(true)
    expect(out.gains[0]).toBe(5)
  })

  it('clamps values that came from outside', () => {
    const out = normaliseSettings({ gains: [99], preamp: 99 })
    expect(out.gains[0]).toBe(MAX_BAND_DB)
    expect(out.preamp).toBe(0)
  })

  it('calls an unrecognised preset name custom rather than dropping the curve', () => {
    const out = normaliseSettings({
      enabled: true,
      gains: BANDS.map(() => 2),
      preamp: -2,
      preset: 'nonsense' as never,
    })
    expect(out.preset).toBe('custom')
    expect(out.gains[0]).toBe(2)
  })

  it('returns something flat for nothing at all', () => {
    const out = normaliseSettings(null)
    expect(out.enabled).toBe(false)
    expect(out.gains).toHaveLength(BANDS.length)
    expect(out.gains.every((g) => g === 0)).toBe(true)
  })
})

describe('dbToGain', () => {
  it('leaves unity alone', () => {
    expect(dbToGain(0)).toBe(1)
  })

  it('halves the amplitude at −6 dB', () => {
    expect(dbToGain(-6)).toBeCloseTo(0.501, 3)
  })
})
