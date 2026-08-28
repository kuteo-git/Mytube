import { useCallback, useState } from 'react'

const STORAGE_KEY = 'autoplay'

/**
 * Whether finishing a video should start the next one.
 *
 * Persisted because it is a standing preference, not a per-video choice — and
 * because the cost of getting it wrong is asymmetric here: every autoplayed
 * video is a fresh download onto a disk with a hard ceiling, so a viewer who
 * turned this off must find it still off tomorrow.
 */
export function useAutoplayPreference(): [boolean, (next: boolean) => void] {
  const [enabled, setEnabled] = useState(() => {
    return window.localStorage.getItem(STORAGE_KEY) !== 'off'
  })

  const update = useCallback((next: boolean) => {
    window.localStorage.setItem(STORAGE_KEY, next ? 'on' : 'off')
    setEnabled(next)
  }, [])

  return [enabled, update]
}

const CHAIN_KEY = 'autoplay-chain'

/**
 * Videos autoplay may chain through with nobody touching anything.
 *
 * Was three, which is what an album feels like when it stops after two songs.
 * The guard exists so an empty room does not download all night, and three was
 * chosen before eviction was running; now that a sweep caps the library at a
 * fixed size, the worst an unattended chain can do is churn within that budget
 * rather than fill the disk. Fifty is long enough to be a listening session and
 * short enough that a forgotten tab does not run for days.
 *
 * Any interaction at all — play, pause, seek, volume, pressing Next — resets
 * the count, so this only ever limits genuinely unattended playback.
 */
const MAX_CHAIN = 50

/** Counts videos played in an unbroken autoplay chain. */
export function autoplayChainLength(): number {
  return Number(window.sessionStorage.getItem(CHAIN_KEY) ?? '0')
}

export function recordAutoplayHop(): void {
  window.sessionStorage.setItem(CHAIN_KEY, String(autoplayChainLength() + 1))
}

export function resetAutoplayChain(): void {
  window.sessionStorage.removeItem(CHAIN_KEY)
}

export function autoplayChainExhausted(): boolean {
  return autoplayChainLength() >= MAX_CHAIN
}

const QUALITY_KEY = 'quality'

/**
 * What the viewer asked for: `'auto'`, or the height of one rung of the ladder.
 *
 * It was `'auto' | 'high' | 'low'`, from a time when the ladder was a label
 * rather than a real thing — `'high'` meant 1080 and `'low'` named the
 * progressive rendition that stopped serving, so it had been a dead value for a
 * release. A height is what hls.js is actually told, so carrying anything else
 * means translating at every call site and having somewhere for the translation
 * to be wrong.
 */
export type QualityChoice = 'auto' | number

/**
 * The rungs the menu will name, best first.
 *
 * The ladder itself goes lower — 360 and 240 exist so ABR has somewhere to go on
 * a bad minute — but nobody chooses those by hand. They are an escape, not a
 * preference, and a menu row for one is a row whose only honest use is admitting
 * the connection is bad.
 */
export const OFFERED_HEIGHTS = [2160, 1440, 1080, 720, 480]

/**
 * What a rung is called on screen.
 *
 * "4K" and "2K" rather than "2160p" and "1440p", because those are the names
 * people actually use for them and the ones every other player shows. Below
 * that the p-form is what everybody says, so it stays: nobody calls 1080p "2K"
 * even though the arithmetic almost allows it.
 *
 * A function rather than a table beside the heights, so a rung the ladder gains
 * later gets a name automatically instead of a blank.
 */
export function labelForHeight(height: number): string {
  if (height >= 2160) return '4K'
  if (height >= 1440) return '2K'
  return `${height}p`
}

/**
 * The ceiling on a phone.
 *
 * An iPhone 16e is 2532x1170, so 720p already exceeds it across the long edge;
 * everything above is bytes spent on pixels the screen cannot draw, over the one
 * road (googlevideo to this gateway) that is measured to be refused in waves.
 *
 * **Enforced by the server, not here.** On iOS, HLS plays natively and a page
 * has no way to pin or limit a level — Safari picks from whatever ladder it is
 * handed. So this number travels as `?max=` on the master playlist URL and the
 * rungs above it are never written. A condition in this file would work on
 * Chrome and do nothing on the device it is for.
 */
export const PHONE_MAX_HEIGHT = 720

/** Heights the app will accept from storage or a menu. */
const KNOWN_HEIGHTS = [...OFFERED_HEIGHTS, 360, 240]

/**
 * What a stored preference from before heights existed meant.
 *
 * Mapped rather than discarded. Somebody in the house has already pinned a
 * rendition, and silently resetting them to Auto is a change they did not ask
 * for and would have no way to explain.
 */
export function migrate(stored: string | null): QualityChoice {
  if (stored === 'high') return 1080
  if (stored === 'low') return 360
  const height = Number(stored)
  return KNOWN_HEIGHTS.includes(height) ? height : 'auto'
}

/**
 * The viewer's quality choice, remembered across videos.
 *
 * Persisted for the same reason autoplay is: it is a standing preference about
 * how this system should behave, not a decision about one video. Someone on a
 * slow connection who picked the low rendition should not have to pick it again
 * for every track.
 *
 * A pinned choice is a command rather than a hint — the player does not climb
 * away from it when a better source appears, and does not retreat from it when
 * the connection struggles. Only "auto" moves on its own.
 */
export function useQualityPreference(): [QualityChoice, (next: QualityChoice) => void] {
  const [choice, setChoice] = useState<QualityChoice>(() => {
    try {
      return migrate(window.localStorage.getItem(QUALITY_KEY))
    } catch {
      return 'auto'
    }
  })

  const update = useCallback((next: QualityChoice) => {
    try {
      window.localStorage.setItem(QUALITY_KEY, String(next))
    } catch {
      // A device that refuses storage still gets the choice for this sitting.
    }
    setChoice(next)
  }, [])

  return [choice, update]
}
