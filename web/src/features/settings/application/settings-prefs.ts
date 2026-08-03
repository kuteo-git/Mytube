/**
 * Narration preferences that belong to this device.
 *
 * Volume is per-device by nature — headphones are not a television — and so is
 * the voice, which is a listening preference rather than a system setting. The
 * translation configuration is the opposite and lives on the server, since it
 * configures a process the whole house shares.
 */

const VOICE_KEY = 'yt-narration-voice-v1'
const VOICE_LEVEL_KEY = 'yt-narration-voice-level-v1'
const DUCK_LEVEL_KEY = 'yt-narration-duck-level-v1'

/** What shipped before any of this was adjustable. */
export const DEFAULT_VOICE = 'Ngọc Linh'
export const DEFAULT_VOICE_LEVEL = 0.5
export const DEFAULT_DUCK_LEVEL = 0.2

/** The voice may go past full; TTS is quieter than film audio and a limiter follows. */
export const MAX_VOICE_LEVEL = 3

export interface NarrationAudioPrefs {
  voice: string
  /** Voice loudness as a fraction of the player's volume. */
  voiceLevel: number
  /** What the video drops to while the voice speaks. */
  duckLevel: number
}

function readLevel(key: string, fallback: number, max: number): number {
  const raw = window.localStorage.getItem(key)
  if (raw === null) return fallback
  const n = Number(raw)
  // A stored value that is not a number, or is out of range, is a value from a
  // version that meant something else by it. The default is safer than honouring
  // it, and silently correcting beats a player that will not make a sound.
  if (!Number.isFinite(n) || n < 0 || n > max) return fallback
  return n
}

export function loadNarrationAudioPrefs(): NarrationAudioPrefs {
  return {
    voice: window.localStorage.getItem(VOICE_KEY) || DEFAULT_VOICE,
    voiceLevel: readLevel(VOICE_LEVEL_KEY, DEFAULT_VOICE_LEVEL, MAX_VOICE_LEVEL),
    duckLevel: readLevel(DUCK_LEVEL_KEY, DEFAULT_DUCK_LEVEL, 1),
  }
}

export function saveNarrationAudioPrefs(p: NarrationAudioPrefs) {
  window.localStorage.setItem(VOICE_KEY, p.voice)
  window.localStorage.setItem(VOICE_LEVEL_KEY, String(p.voiceLevel))
  window.localStorage.setItem(DUCK_LEVEL_KEY, String(p.duckLevel))
}
