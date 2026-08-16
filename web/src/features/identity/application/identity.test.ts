import { beforeEach, describe, expect, it, vi, afterEach } from 'vitest'

import { apiFetch, apiJSON } from '@/shared/api/http'
import {
  currentProfileID,
  setCurrentProfileID,
} from '../infrastructure/current-profile'
import { NO_PROFILE, validProfileName } from '../domain/profile'

function lastHeaders(): Headers {
  const call = vi.mocked(fetch).mock.calls.at(-1)
  return new Headers((call?.[1] as RequestInit | undefined)?.headers)
}

beforeEach(() => {
  window.localStorage.clear()
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('{}', { status: 200 })),
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('who this browser is', () => {
  it('is nobody until somebody chooses', () => {
    expect(currentProfileID()).toBe(NO_PROFILE)
  })

  it('remembers the choice', () => {
    setCurrentProfileID('u_vo')
    expect(currentProfileID()).toBe('u_vo')
  })

  it('forgets it again', () => {
    setCurrentProfileID('u_vo')
    setCurrentProfileID(NO_PROFILE)
    expect(currentProfileID()).toBe(NO_PROFILE)
  })
})

describe('every request says who is asking', () => {
  it('carries the profile once one is chosen', async () => {
    setCurrentProfileID('u_vo')
    await apiFetch('/api/feed')
    expect(lastHeaders().get('X-User-Id')).toBe('u_vo')
  })

  // The header is omitted rather than sent empty, and the difference matters.
  //
  // An empty X-User-Id is a value, and the gateway would take it at face value.
  // Its *absence* is what triggers the fallback to DEV_USER_ID — which is the
  // only thing keeping an install that predates profiles working exactly as it
  // did, with its whole watch history still attached.
  it('sends no header at all when nobody has chosen', async () => {
    await apiFetch('/api/feed')
    expect(lastHeaders().has('X-User-Id')).toBe(false)
  })

  it('keeps the headers the caller asked for', async () => {
    setCurrentProfileID('u_luc')
    await apiFetch('/api/feed', { headers: { 'X-Test': 'kept' } })
    const headers = lastHeaders()
    expect(headers.get('X-Test')).toBe('kept')
    expect(headers.get('X-User-Id')).toBe('u_luc')
  })

  it('sets a JSON content type for a body, and not otherwise', async () => {
    await apiJSON('/api/profiles', { method: 'POST', body: '{"name":"A"}' })
    expect(lastHeaders().get('Content-Type')).toBe('application/json')

    await apiJSON('/api/profiles')
    expect(lastHeaders().has('Content-Type')).toBe(false)
  })

  it('throws with the status on a refusal', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('nope', { status: 409 })),
    )
    await expect(apiJSON('/api/profiles')).rejects.toMatchObject({ status: 409 })
  })
})

describe('profile names', () => {
  it('accepts something a person would type', () => {
    expect(validProfileName('Vợ')).toBe(true)
  })

  it('rejects nothing, and rejects an essay', () => {
    expect(validProfileName('')).toBe(false)
    expect(validProfileName('   ')).toBe(false)
    expect(validProfileName('a'.repeat(41))).toBe(false)
  })
})
