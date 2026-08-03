/**
 * The finished translation, written back out as a subtitle file.
 *
 * The cache in narration.vi.json is keyed by the hash of each cue's text, which
 * is what lets it survive the cue grouping being retuned — but it is not
 * something anything else can read. A VTT is: `collectSubtitles` in the ingest
 * service treats every *.vtt in a video's folder as a track, VLC opens it, and
 * a TV can display it.
 *
 * So the two coexist rather than compete. The JSON is the cache; this is the
 * artifact, written once the pass has finished and there is a whole file to
 * write rather than a partial one that would look complete.
 */

import type { CueText } from './narration-vtt'

/** `12.5` -> `00:00:12.500`. */
export function formatVTTTime(seconds: number): string {
  const t = Math.max(0, seconds)
  const hh = Math.floor(t / 3600)
  const mm = Math.floor((t % 3600) / 60)
  const ss = Math.floor(t % 60)
  const ms = Math.round((t - Math.floor(t)) * 1000)
  const pad = (n: number, width = 2) => String(n).padStart(width, '0')
  return `${pad(hh)}:${pad(mm)}:${pad(ss)}.${pad(ms, 3)}`
}

/**
 * Build a WebVTT file from cues and their translations.
 *
 * Untranslated cues are left out rather than falling back to the English. A
 * file half in the source language, offered as a Vietnamese track, is worse
 * than a shorter one that is wholly Vietnamese.
 */
export function toVTT(
  cues: Array<Pick<CueText, 'start' | 'end' | 'text'>>,
  translations: Map<string, string>,
): string {
  const blocks: string[] = []
  for (const cue of cues) {
    const vi = translations.get(cue.text)
    if (!vi) continue
    // A blank line inside a cue would end it early and turn the rest into a
    // malformed block, so any run of newlines collapses to a space.
    const body = vi.replace(/\s*\n\s*/g, ' ').trim()
    if (!body) continue
    blocks.push(
      `${formatVTTTime(cue.start)} --> ${formatVTTTime(cue.end)}\n${body}\n`,
    )
  }
  if (blocks.length === 0) return ''
  return `WEBVTT\n\n${blocks.join('\n')}`
}
