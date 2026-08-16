import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ProfilePicker } from './ProfilePicker'
import { apiFetch } from '@/shared/api/http'
import { setCurrentProfileID } from '../infrastructure/current-profile'
import type { Profile } from '../domain/profile'

const household: Profile[] = [
  { id: 'u_luc', name: 'Luc' },
  { id: 'u_lm', name: 'Lượm' },
]

vi.mock('../infrastructure/profileRepository', () => ({
  httpProfileRepository: {
    list: vi.fn(async () => household),
    create: vi.fn(),
  },
}))

function lastUserHeader(): string | null {
  const call = vi.mocked(fetch).mock.calls.at(-1)
  return new Headers((call?.[1] as RequestInit | undefined)?.headers).get('X-User-Id')
}

beforeEach(() => {
  window.localStorage.clear()
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('{}', { status: 200 })),
  )
})

// Picking somebody has to change who the next request is made as.
//
// Written after switching to a second profile still showed the first person's
// feed. The server was already right — the same request with two different
// X-User-Id headers returned two entirely different pages — so the question was
// whether the browser was sending the new one at all.
describe('switching profile', () => {
  it('makes the next request as the person just picked', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    render(
      <QueryClientProvider client={client}>
        <ProfilePicker />
      </QueryClientProvider>,
    )

    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: /Lượm/ }))
    })

    await apiFetch('/api/feed')
    expect(lastUserHeader()).toBe('u_lm')
  })

  // The cache is the other half. Everything the app has fetched is answered per
  // user, and the feed's key is ['feed', topic] with no id in it — so anything
  // left behind would be served to the next person under their own name.
  it('leaves nothing of the previous person in the cache', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    client.setQueryData(['feed', ''], { pages: [{ videos: [{ id: 'lucs-video' }] }] })

    render(
      <QueryClientProvider client={client}>
        <ProfilePicker />
      </QueryClientProvider>,
    )
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: /Lượm/ }))
    })

    await waitFor(() => expect(client.getQueryData(['feed', ''])).toBeUndefined())
  })
})

// The structural half: two people's answers are not the same cache entry.
//
// The keys said nothing about who asked, and switching cleared the whole cache
// to compensate. That worked, but it made keeping two people's data apart
// depend on a side effect firing at the right moment — and when it did not, the
// symptom was somebody seeing another person's feed under their own name with
// nothing on screen to say so. In the key it cannot happen at all.
describe('per-user cache keys', () => {
  it('does not serve one person an entry stored for another', async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    })
    setCurrentProfileID('u_luc')
    const lucsKey = ['feed', 'u_luc', '']
    client.setQueryData(lucsKey, 'luc')

    setCurrentProfileID('u_lm')
    expect(client.getQueryData(['feed', 'u_lm', ''])).toBeUndefined()
    // And the other person's entry is untouched rather than quietly reused.
    expect(client.getQueryData(lucsKey)).toBe('luc')
  })
})
