import { apiFetch, apiJSON, HttpError } from '@/shared/api/http'

/**
 * A household member's YouTube session, from the browser's side.
 *
 * There is no `get cookies`. The API has no such route, and this has no such
 * method: a cookies.txt is a live Google session, and the only place it should
 * ever exist is the file on the server and the yt-dlp command line built from
 * it. What can be read is whether one is there and whether it still works.
 */
export type AccountState = 'OK' | 'EXPIRED' | 'NEVER_SET'

export interface AccountStatus {
  userId: string
  label: string
  state: AccountState
  lastResult?: string
  lastScanAt?: string
}

/**
 * What the pass in flight is doing.
 *
 * Read from the server rather than kept in the browser, which is what makes a
 * page reload mid-pass harmless: a first fill reads every playlist a member has
 * and takes minutes, so the pass outlives whatever started it.
 */
export interface ScanStatus {
  running: boolean
  durationMs: number
  /** In words: "reading playlists (7 of 28)". Empty when idle. */
  phase: string
  playlistsRead: number
  playlistsTotal: number
  accounts: number
  subscriptions: number
  videos: number
  expired: number
  playlists: number
  playlistVideos: number
  /** Failures that did not stop the pass. Bounded server-side. */
  errors: string[]
}

export const accountRepository = {
  async get(): Promise<AccountStatus> {
    const { account } = await apiJSON<{ account: AccountStatus }>(
      '/api/settings/youtube-account',
    )
    return account
  },

  /** Returns the server's own words on a bad paste — it says which way it was wrong. */
  async save(cookies: string, label: string): Promise<void> {
    const response = await apiFetch('/api/settings/youtube-account', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ cookies, label }),
    })
    if (!response.ok) {
      const body = await response.json().catch(() => ({}) as { error?: string })
      throw new HttpError(response.status, body.error || 'save failed')
    }
  },

  async remove(): Promise<void> {
    await apiFetch('/api/settings/youtube-account', { method: 'DELETE' })
  },

  /** Starts a pass. Returns as soon as it has begun — poll scanStatus for the rest. */
  async scanNow(): Promise<void> {
    await apiFetch('/api/settings/youtube-account/scan', { method: 'POST' })
  },

  async scanStatus(): Promise<ScanStatus> {
    return apiJSON<ScanStatus>('/api/settings/youtube-account/scan')
  },
}
