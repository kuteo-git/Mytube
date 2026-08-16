import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { YouTubeAccountSettings } from './YouTubeAccountSettings'
import { CookieExpiryBanner } from './CookieExpiryBanner'
import type { AccountStatus } from '../infrastructure/accountRepository'

let status: AccountStatus
let saveError: string | null = null
const saved: string[] = []

vi.mock('../infrastructure/accountRepository', () => ({
  accountRepository: {
    get: vi.fn(async () => status),
    save: vi.fn(async (cookies: string) => {
      if (saveError) throw new Error(saveError)
      saved.push(cookies)
      status = { ...status, state: 'OK' }
    }),
    remove: vi.fn(async () => {
      status = { ...status, state: 'NEVER_SET' }
    }),
    scanNow: vi.fn(async () => ({
      accounts: 1,
      subscriptions: 42,
      videos: 90,
      expired: 0,
    })),
  },
}))

function renderIt(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  status = { userId: 'u_luc', label: '', state: 'NEVER_SET' }
  saveError = null
  saved.length = 0
  vi.clearAllMocks()
})

describe('connecting an account', () => {
  it('sends the paste and then holds none of it', async () => {
    renderIt(<YouTubeAccountSettings />)
    const box = await screen.findByLabelText('Cookies file')

    await act(async () => {
      fireEvent.change(box, { target: { value: '# Netscape HTTP Cookie File\nrow' } })
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Connect' }))
    })

    expect(saved).toHaveLength(1)
    // Cleared the moment the server accepts it. Leaving a live Google session
    // sitting in component state for the rest of the sitting is the one thing
    // this screen can still get wrong after the paste has worked.
    await waitFor(() => expect((box as HTMLTextAreaElement).value).toBe(''))
  })

  it('shows the server own words on a bad paste', async () => {
    saveError = 'this does not look like a cookies.txt'
    renderIt(<YouTubeAccountSettings />)

    await act(async () => {
      fireEvent.change(await screen.findByLabelText('Cookies file'), {
        target: { value: '{"json": true}' },
      })
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Connect' }))
    })

    expect(await screen.findByRole('alert')).toHaveTextContent('does not look like a cookies.txt')
  })

  it('will not send an empty box', async () => {
    renderIt(<YouTubeAccountSettings />)
    expect(await screen.findByRole('button', { name: 'Connect' })).toBeDisabled()
  })

  // The mistake this page could cause is worse than any it could suffer:
  // "Get cookies.txt" without LOCALLY was pulled from the Chrome Web Store as
  // malware, and a reader is about to hand one of them a Google session.
  it('names the right extension and warns about the wrong one', async () => {
    renderIt(<YouTubeAccountSettings />)
    expect(await screen.findByText('Get cookies.txt LOCALLY')).toBeInTheDocument()
    expect(screen.getByText(/removed\s+from the store as malware/)).toBeInTheDocument()
  })

  it('offers nothing to disconnect before anything is connected', async () => {
    renderIt(<YouTubeAccountSettings />)
    await screen.findByLabelText('Cookies file')
    expect(screen.queryByRole('button', { name: 'Disconnect' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Scan now' })).toBeNull()
  })
})

describe('a session that has ended', () => {
  beforeEach(() => {
    status = { userId: 'u_luc', label: '', state: 'EXPIRED' }
  })

  it('says so on the settings screen', async () => {
    renderIt(<YouTubeAccountSettings />)
    expect(await screen.findByText(/paste your cookies again/)).toBeInTheDocument()
  })

  it('raises a banner with a way back', async () => {
    renderIt(<CookieExpiryBanner />)
    expect(await screen.findByRole('status')).toHaveTextContent(/signed you out/)
    expect(screen.getByRole('link', { name: 'Reconnect' })).toHaveAttribute(
      'href',
      '/settings/youtube-account',
    )
  })
})

// A banner that is up while things work is a banner nobody reads on the day
// they need to.
describe('a session that works', () => {
  it('raises no banner', async () => {
    status = { userId: 'u_luc', label: '', state: 'OK' }
    renderIt(<CookieExpiryBanner />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
  })

  it('raises none before an account exists either', async () => {
    renderIt(<CookieExpiryBanner />)
    await waitFor(() => expect(screen.queryByRole('status')).toBeNull())
  })
})
