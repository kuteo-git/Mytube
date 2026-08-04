/**
 * What narration does, now that the translation is a subtitle track like any
 * other.
 *
 * There used to be four output modes here — off, subtitles, voice, both —
 * because showing the translated text was something only this feature could do,
 * by drawing over the picture. Once the translation is a track the browser can
 * render, "show it" is just selecting it in the subtitle list, and what is left
 * for narration to decide is whether to read it aloud.
 *
 * There was also an "auto translate" switch, and it is gone (2026-08-04). It
 * described nothing a viewer decides: a translation is wanted when the track is
 * selected or when something has to be read aloud, and both of those are said
 * elsewhere, plainly. Left as a switch it was a third way of asking that only
 * modified the other two — on by default, so pressing it turned translation
 * *off*, and pressing it again appeared to do nothing at all.
 */

const SPEAK_KEY = 'yt-narration-speak-v1'
/** Superseded twice. Read once, to carry a viewer's choice across. */
const OUTPUT_KEY = 'yt-narration-output-v1'
const LEGACY_ON_KEY = 'yt-narration-on'

export function loadNarrationPrefs(): { speak: boolean } {
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
  return { speak }
}

export function saveNarrationPrefs(p: { speak: boolean }) {
  window.localStorage.setItem(SPEAK_KEY, p.speak ? '1' : '0')
}
