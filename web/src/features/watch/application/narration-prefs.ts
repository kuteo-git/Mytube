/**
 * What narration does, now that the translation is a subtitle track like any
 * other.
 *
 * There used to be four output modes here — off, subtitles, voice, both —
 * because showing the translated text was something only this feature could do,
 * by drawing over the picture. Once the translation is a track the browser can
 * render, "show it" is just selecting it in the subtitle list, and what is left
 * for narration to decide is whether to read it aloud.
 */

const SPEAK_KEY = 'yt-narration-speak-v1'
const AUTO_KEY = 'yt-narration-auto-translate-v1'
/** Superseded twice. Read once, to carry a viewer's choice across. */
const OUTPUT_KEY = 'yt-narration-output-v1'
const LEGACY_ON_KEY = 'yt-narration-on'

export function loadNarrationPrefs(): {
  speak: boolean
  /** Whether the background translation pass may run at all. */
  autoTranslate: boolean
} {
  const raw = window.localStorage.getItem(SPEAK_KEY)
  let speak = raw === '1'
  if (raw === null) {
    // Anyone who had it reading aloud under either older key keeps that.
    const output = window.localStorage.getItem(OUTPUT_KEY)
    speak =
      output === 'voice' ||
      output === 'both' ||
      window.localStorage.getItem(LEGACY_ON_KEY) === '1'
  }

  // Defaults on: a video with only English subtitles cannot be narrated without
  // it, and someone switching narration on has already said what they want.
  const autoTranslate = window.localStorage.getItem(AUTO_KEY) !== '0'
  return { speak, autoTranslate }
}

export function saveNarrationPrefs(p: {
  speak: boolean
  autoTranslate: boolean
}) {
  window.localStorage.setItem(SPEAK_KEY, p.speak ? '1' : '0')
  window.localStorage.setItem(AUTO_KEY, p.autoTranslate ? '1' : '0')
}
