import { beforeEach, describe, expect, it } from 'vitest'
import { loadNarrationPrefs, saveNarrationPrefs } from './narration-prefs'

beforeEach(() => window.localStorage.clear())

describe('loadNarrationPrefs', () => {
  it('defaults to off', () => {
    expect(loadNarrationPrefs()).toEqual({ output: 'off' })
  })

  it('migrates the old on/off switch to spoken output', () => {
    window.localStorage.setItem('yt-narration-on', '1')
    expect(loadNarrationPrefs().output).toBe('voice')
  })

  it('does not turn narration on for someone who had it off', () => {
    window.localStorage.setItem('yt-narration-on', '0')
    expect(loadNarrationPrefs().output).toBe('off')
  })

  it('ignores a value that is not a valid choice', () => {
    window.localStorage.setItem('yt-narration-output-v1', 'shout')
    expect(loadNarrationPrefs()).toEqual({ output: 'off' })
  })

  it('round-trips what was saved', () => {
    saveNarrationPrefs({ output: 'both' })
    expect(loadNarrationPrefs()).toEqual({ output: 'both' })
  })
})
