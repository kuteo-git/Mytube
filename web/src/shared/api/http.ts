/**
 * The one place a request to the gateway is made.
 *
 * It exists for a single reason: every call has to say who is asking. The
 * gateway reads `X-User-Id` and falls back to a configured default when the
 * header is absent — which for the whole life of this app it always was, so
 * every browser in the household was the same person and every subscription,
 * like and watch signal landed on one id.
 *
 * Attached here rather than at each call site because "every request" is the
 * requirement, and a rule that has to be remembered forty times is a rule that
 * will be forgotten once. The repositories in `infrastructure/` call this;
 * `ui/` still calls nothing (§5).
 */

import { currentProfileID } from '@/features/identity/infrastructure/current-profile'

export class HttpError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
    this.name = 'HttpError'
  }
}

/**
 * fetch, carrying the profile.
 *
 * The header is omitted rather than sent empty when nobody has chosen. An empty
 * `X-User-Id` is a value, and the gateway would take it at face value: the
 * absence is what triggers its fallback, and that fallback is what keeps an
 * install that predates profiles working exactly as it did.
 */
export function apiFetch(input: string, init?: RequestInit): Promise<Response> {
  const profile = currentProfileID()
  const headers = new Headers(init?.headers)
  if (profile) headers.set('X-User-Id', profile)
  return fetch(input, { ...init, headers })
}

/** apiFetch, with JSON in and out and a thrown error on a bad status. */
export async function apiJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (init?.body && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }
  const response = await apiFetch(path, { ...init, headers })
  if (!response.ok) {
    const detail = await response.text().catch(() => '')
    throw new HttpError(response.status, detail || response.statusText)
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}
