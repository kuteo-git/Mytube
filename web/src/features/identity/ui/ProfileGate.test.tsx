import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ProfileGate } from './ProfileGate'
import { setCurrentProfileID } from '../infrastructure/current-profile'
import type { Profile } from '../domain/profile'

let household: Profile[] = []

vi.mock('../infrastructure/profileRepository', () => ({
  httpProfileRepository: {
    list: vi.fn(async () => household),
    create: vi.fn(async (name: string) => {
      const created = { id: 'u_new', name }
      household = [...household, created]
      return created
    }),
  },
}))

function renderGate() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  })
  render(
    <QueryClientProvider client={client}>
      <ProfileGate>
        <div>the app</div>
      </ProfileGate>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  window.localStorage.clear()
  household = [{ id: 'u_luc', name: 'Luc' }]
})

afterEach(() => {
  vi.clearAllMocks()
})

// One person has no question to answer.
//
// A gate in front of a household of one is a screen guarding an empty room —
// and the gateway already falls back to the configured user when no header
// arrives, so a browser that has never chosen behaves exactly as every browser
// did before profiles existed.
describe('a household of one', () => {
  it('is never asked', async () => {
    renderGate()
    expect(await screen.findByText('the app')).toBeInTheDocument()
    expect(screen.queryByText(/Who's watching/)).toBeNull()
  })
})

describe('a household of two', () => {
  beforeEach(() => {
    household = [
      { id: 'u_luc', name: 'Luc' },
      { id: 'u_vo', name: 'Vợ' },
    ]
  })

  it('asks a browser that has not chosen', async () => {
    renderGate()
    expect(await screen.findByText(/Who's watching/)).toBeInTheDocument()
    expect(screen.queryByText('the app')).toBeNull()
  })

  it('lets the app through once somebody is picked', async () => {
    renderGate()
    const button = await screen.findByRole('button', { name: /Vợ/ })
    await act(async () => {
      fireEvent.click(button)
    })
    expect(await screen.findByText('the app')).toBeInTheDocument()
  })

  it('does not ask again on a browser that already chose', async () => {
    setCurrentProfileID('u_luc')
    renderGate()
    expect(await screen.findByText('the app')).toBeInTheDocument()
  })

  // Being briefly wrong about who is asking costs a refetch. A blank screen on
  // every cold load costs the whole first impression.
  it('shows the app rather than a spinner while the list loads', () => {
    renderGate()
    expect(screen.getByText('the app')).toBeInTheDocument()
  })
})

describe('adding somebody', () => {
  beforeEach(() => {
    household = [
      { id: 'u_luc', name: 'Luc' },
      { id: 'u_vo', name: 'Vợ' },
    ]
  })

  it('creates the profile and becomes it', async () => {
    renderGate()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Add someone' }))
    })
    await act(async () => {
      fireEvent.change(screen.getByLabelText('New profile name'), {
        target: { value: 'Minh' },
      })
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Add' }))
    })

    await waitFor(() => expect(screen.getByText('the app')).toBeInTheDocument())
  })

  it('refuses a blank name without calling the server', async () => {
    const { httpProfileRepository } = await import('../infrastructure/profileRepository')
    renderGate()
    await act(async () => {
      fireEvent.click(await screen.findByRole('button', { name: 'Add someone' }))
    })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Add' }))
    })

    expect(screen.getByText('Enter a name')).toBeInTheDocument()
    expect(httpProfileRepository.create).not.toHaveBeenCalled()
  })
})
