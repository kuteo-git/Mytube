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

export interface ScanResult {
  accounts: number
  subscriptions: number
  videos: number
  expired: number
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
      throw new HttpError(response.status, body.error || 'Could not save')
    }
  },

  async remove(): Promise<void> {
    await apiFetch('/api/settings/youtube-account', { method: 'DELETE' })
  },

  async scanNow(): Promise<ScanResult> {
    return apiJSON<ScanResult>('/api/settings/youtube-account/scan', { method: 'POST' })
  },
}
