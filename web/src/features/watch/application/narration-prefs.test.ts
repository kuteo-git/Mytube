import { beforeEach, describe, expect, it } from 'vitest'
import { loadNarrationPrefs, saveNarrationPrefs } from './narration-prefs'

beforeEach(() => window.localStorage.clear())

describe('loadNarrationPrefs', () => {
  it('defaults to off, on the router', () => {
    // Omniroute by default: it translates as well as the local model and does
    // not compete for the GPU that yt-dlp and ffmpeg are already using every
    // time someone presses play.
    expect(loadNarrationPrefs()).toEqual({ engine: 'omniroute', output: 'off' })
  })

  it('migrates the old on/off switch to spoken output', () => {
    window.localStorage.setItem('yt-narration-on', '1')
    expect(loadNarrationPrefs().output).toBe('voice')
  })

  it('does not turn narration on for someone who had it off', () => {
    window.localStorage.setItem('yt-narration-on', '0')
    expect(loadNarrationPrefs().output).toBe('off')
  })

  it('ignores values that are not valid choices', () => {
    window.localStorage.setItem('yt-narration-engine-v1', 'gemma')
    window.localStorage.setItem('yt-narration-output-v1', 'shout')
    expect(loadNarrationPrefs()).toEqual({ engine: 'omniroute', output: 'off' })
  })

  it('round-trips what was saved', () => {
    saveNarrationPrefs({ engine: 'nllb', output: 'both' })
    expect(loadNarrationPrefs()).toEqual({ engine: 'nllb', output: 'both' })
  })
})
