import { beforeEach, describe, expect, it } from 'vitest'
import { loadNarrationPrefs, saveNarrationPrefs } from './narration-prefs'

beforeEach(() => window.localStorage.clear())

describe('loadNarrationPrefs', () => {
  it('defaults to silent', () => {
    // Sound out of a page nobody asked to speak is a fright. Translation has no
    // preference of its own any more: it is asked for by choosing the track or
    // by switching this on.
    expect(loadNarrationPrefs()).toEqual({ speak: false })
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

  it('round-trips what was saved', () => {
    saveNarrationPrefs({ speak: true })
    expect(loadNarrationPrefs()).toEqual({ speak: true })
  })

  it('ignores a stored auto-translate choice', () => {
    // The switch is gone: translation is asked for by choosing the track or by
    // asking for the narration. A value left behind by an older build must not
    // come back to life as a preference nothing can see or change.
    window.localStorage.setItem('yt-narration-auto-translate-v1', '0')
    saveNarrationPrefs({ speak: true })
    expect(loadNarrationPrefs()).toEqual({ speak: true })
  })
})
