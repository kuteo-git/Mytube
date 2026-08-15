import { beforeEach, describe, expect, it } from 'vitest'

import { loadEqSettings, saveEqSettings } from './eq-prefs'
import { BANDS, settingsForPreset } from './eq-presets'

const KEY = 'yt-equalizer-v1'

beforeEach(() => {
  window.localStorage.clear()
})

describe('eq preferences', () => {
  it('comes back the way it went in', () => {
    const settings = settingsForPreset('bass')
    saveEqSettings(settings)
    expect(loadEqSettings()).toEqual(settings)
  })

  it('starts flat and switched off when nothing is stored', () => {
    const out = loadEqSettings()
    expect(out.enabled).toBe(false)
    expect(out.gains).toHaveLength(BANDS.length)
    expect(out.gains.every((g) => g === 0)).toBe(true)
  })

  it('starts flat rather than throwing on a value it cannot parse', () => {
    // Hand-editable by design, and written by whatever version came before.
    // Refusing to load would take the player's sound out over a stored string.
    window.localStorage.setItem(KEY, '{not json')
    expect(loadEqSettings().enabled).toBe(false)
  })

  it('brings a stored curve back into range', () => {
    window.localStorage.setItem(
      KEY,
      JSON.stringify({ enabled: true, gains: [99], preamp: 20, preset: 'custom' }),
    )
    const out = loadEqSettings()
    expect(out.gains[0]).toBe(12)
    expect(out.preamp).toBe(0)
    expect(out.gains).toHaveLength(BANDS.length)
  })
})
