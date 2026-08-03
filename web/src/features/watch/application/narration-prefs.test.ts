import { beforeEach, describe, expect, it } from 'vitest'
import { loadNarrationPrefs, saveNarrationPrefs } from './narration-prefs'

beforeEach(() => window.localStorage.clear())

describe('loadNarrationPrefs', () => {
  it('defaults to silent, with translation allowed', () => {
    // A video with only English subtitles cannot be narrated at all without
    // translation, so the switch that governs it starts on.
    expect(loadNarrationPrefs()).toEqual({ speak: false, autoTranslate: true })
  })

  it('carries across someone who had a voice under the old output setting', () => {
    window.localStorage.setItem('yt-narration-output-v1', 'both')
    expect(loadNarrationPrefs().speak).toBe(true)
  })

  it('carries across the oldest on/off switch too', () => {
    window.localStorage.setItem('yt-narration-on', '1')
    expect(loadNarrationPrefs().speak).toBe(true)
  })

  it('does not start talking to someone who only ever wanted subtitles', () => {
    window.localStorage.setItem('yt-narration-output-v1', 'subs')
    expect(loadNarrationPrefs().speak).toBe(false)
  })

  it('prefers its own key once there is one', () => {
    window.localStorage.setItem('yt-narration-output-v1', 'both')
    window.localStorage.setItem('yt-narration-speak-v1', '0')
    expect(loadNarrationPrefs().speak).toBe(false)
  })

  it('remembers translation being switched off', () => {
    saveNarrationPrefs({ speak: true, autoTranslate: false })
    expect(loadNarrationPrefs().autoTranslate).toBe(false)
  })

  it('round-trips what was saved', () => {
    saveNarrationPrefs({ speak: true, autoTranslate: true })
    expect(loadNarrationPrefs()).toEqual({ speak: true, autoTranslate: true })
  })
})
