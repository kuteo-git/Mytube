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

/** What the viewer asked for. "auto" lets the player choose and change. */
export type QualityChoice = 'auto' | 'high' | 'low'

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
    const stored = window.localStorage.getItem(QUALITY_KEY)
    return stored === 'high' || stored === 'low' ? stored : 'auto'
  })

  const update = useCallback((next: QualityChoice) => {
    window.localStorage.setItem(QUALITY_KEY, next)
    setChoice(next)
  }, [])

  return [choice, update]
}
