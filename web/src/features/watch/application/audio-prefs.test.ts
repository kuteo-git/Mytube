import { beforeEach, describe, expect, it } from 'vitest'

import { loadAudioSettings, saveAudioSettings } from './audio-prefs'
import { BANDS, settingsForPreset } from './eq-presets'
import { REVERB_OFF } from './reverb-presets'

const KEY = 'yt-equalizer-v1'

beforeEach(() => {
  window.localStorage.clear()
})

describe('audio preferences', () => {
  it('comes back the way it went in', () => {
    const settings = {
      eq: settingsForPreset('bass'),
      reverb: { enabled: true, preset: 'hall' as const, wet: 0.4 },
    }
    saveAudioSettings(settings)
    expect(loadAudioSettings()).toEqual(settings)
  })

  it('starts flat, dry and switched off', () => {
    const out = loadAudioSettings()
    expect(out.eq.enabled).toBe(false)
    expect(out.eq.gains).toHaveLength(BANDS.length)
    expect(out.reverb).toEqual(REVERB_OFF)
  })

  it('reads a curve stored before the reverb existed', () => {
    // The old shape was the equaliser's own object at the top level. A new key
    // would have been tidier and would have thrown away every curve already
    // saved on every device.
    window.localStorage.setItem(
      KEY,
      JSON.stringify({
        enabled: true,
        gains: [6, 5, 4, 2, 0, 0, 0, 0, 0, 0],
        preamp: -6,
        preset: 'bass',
      }),
    )
    const out = loadAudioSettings()
    expect(out.eq.enabled).toBe(true)
    expect(out.eq.preset).toBe('bass')
    expect(out.eq.gains[0]).toBe(6)
    expect(out.reverb.enabled).toBe(false)
  })

  it('starts fresh rather than throwing on a value it cannot parse', () => {
    window.localStorage.setItem(KEY, '{not json')
    expect(loadAudioSettings().eq.enabled).toBe(false)
  })

  it('brings stored values back into range', () => {
    window.localStorage.setItem(
      KEY,
      JSON.stringify({
        eq: { enabled: true, gains: [99], preamp: 20, preset: 'custom' },
        reverb: { enabled: true, preset: 'nowhere', wet: 9 },
      }),
    )
    const out = loadAudioSettings()
    expect(out.eq.gains[0]).toBe(12)
    expect(out.eq.preamp).toBe(0)
    expect(out.reverb.wet).toBe(1)
    expect(out.reverb.preset).toBe(REVERB_OFF.preset)
  })
})
