import { useEffect, useState } from 'react'
import {
  currentCueText,
  narrationCues,
  translatedCue,
} from '@/features/watch/application/narration'

/**
 * Draws the machine translation of the line currently being spoken.
 *
 * An overlay rather than a <track>: these cues are translated in the browser
 * and never become a subtitle file, so there is nothing for the caption
 * renderer to be handed.
 *
 * Nothing is drawn until the translation exists. Showing the English would be
 * showing the wrong language under a Vietnamese-subtitles setting, and showing
 * a placeholder would flicker on every cue the background pass has not reached.
 */
export function NarrationSubtitles({
  front,
  active,
}: {
  /**
   * Asked for the element on every tick rather than handed one. The player
   * keeps two <video> layers and swaps which is in front; a captured element
   * would keep reading the currentTime of the layer that is no longer showing.
   */
  front: () => HTMLVideoElement | null
  active: boolean
}) {
  const [line, setLine] = useState<string | null>(null)

  useEffect(() => {
    if (!active) {
      setLine(null)
      return
    }
    // Four times a second: fast enough that a line lands with the voice, slow
    // enough to stay off the render path of a playing video.
    const id = window.setInterval(() => {
      const el = front()
      if (!el) return
      const en = currentCueText(narrationCues(), el.currentTime)
      setLine(en ? (translatedCue(en) ?? null) : null)
    }, 250)
    return () => window.clearInterval(id)
  }, [active, front])

  if (!line) return null

  // Deliberately matched to the ::cue rules in index.css: the video's own
  // subtitles are drawn by the browser and this by us, and without one spec
  // written in both places the text changes font, size and height when the
  // viewer switches between them.
  return (
    <div className="pointer-events-none absolute inset-x-0 bottom-[8%] z-20 flex justify-center px-[5%]">
      <span
        className="rounded bg-black/70 px-2 py-0.5 text-center text-white"
        style={{ fontSize: '3.2vh', lineHeight: 1.35 }}
      >
        {line}
      </span>
    </div>
  )
}
