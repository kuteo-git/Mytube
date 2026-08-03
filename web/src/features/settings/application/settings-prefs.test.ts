import { beforeEach, describe, expect, it } from 'vitest'
import {
  DEFAULT_DUCK_LEVEL,
  DEFAULT_VOICE,
  DEFAULT_VOICE_LEVEL,
  loadNarrationAudioPrefs,
  saveNarrationAudioPrefs,
} from './settings-prefs'

beforeEach(() => window.localStorage.clear())

describe('loadNarrationAudioPrefs', () => {
  it('defaults to exactly what shipped before', () => {
    // These two numbers are the old constants' effective values, so nobody's
    // balance changes when the sliders arrive.
    expect(loadNarrationAudioPrefs()).toEqual({
      voice: DEFAULT_VOICE,
      voiceLevel: DEFAULT_VOICE_LEVEL,
      duckLevel: DEFAULT_DUCK_LEVEL,
    })
  })

  it('round-trips a saved choice', () => {
    saveNarrationAudioPrefs({ voice: 'Gia Bảo', voiceLevel: 1.5, duckLevel: 0.4 })
    expect(loadNarrationAudioPrefs()).toEqual({
      voice: 'Gia Bảo',
      voiceLevel: 1.5,
      duckLevel: 0.4,
    })
  })

  it('accepts a voice above full', () => {
    saveNarrationAudioPrefs({ voice: 'X', voiceLevel: 2.5, duckLevel: 0.2 })
    expect(loadNarrationAudioPrefs().voiceLevel).toBe(2.5)
  })

  it('falls back rather than trusting a value it cannot use', () => {
    // Anything unparseable, negative, or past the ceiling came from a version
    // that meant something else by it. Silently correcting beats a player that
    // will not make a sound.
    window.localStorage.setItem('yt-narration-voice-level-v1', 'loud')
    expect(loadNarrationAudioPrefs().voiceLevel).toBe(DEFAULT_VOICE_LEVEL)

    window.localStorage.setItem('yt-narration-voice-level-v1', '-1')
    expect(loadNarrationAudioPrefs().voiceLevel).toBe(DEFAULT_VOICE_LEVEL)

    window.localStorage.setItem('yt-narration-voice-level-v1', '99')
    expect(loadNarrationAudioPrefs().voiceLevel).toBe(DEFAULT_VOICE_LEVEL)
  })

  it('keeps the video level inside its own ceiling', () => {
    // The video cannot go above the player's own volume; only the voice can.
    window.localStorage.setItem('yt-narration-duck-level-v1', '2')
    expect(loadNarrationAudioPrefs().duckLevel).toBe(DEFAULT_DUCK_LEVEL)
  })

  it('takes zero as a real choice', () => {
    // Zero voice is "show the subtitles, say nothing"; zero video is "only the
    // narration". Both are things people ask for, and neither is a missing value.
    saveNarrationAudioPrefs({ voice: 'X', voiceLevel: 0, duckLevel: 0 })
    const got = loadNarrationAudioPrefs()
    expect(got.voiceLevel).toBe(0)
    expect(got.duckLevel).toBe(0)
  })
})
