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
const MAX_CHAIN = 3

/**
 * Counts videos played in an unbroken autoplay chain. The chain is what stops
 * an empty room from downloading all night: after three hops with no human
 * input, the next hop does not happen.
 */
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
