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

/**
 * A stored id whose profile has been deleted must not keep being sent.
 *
 * The gate asked only whether *an* id had been chosen, never whether it still
 * named anybody. So a device left holding a deleted profile's id kept putting it
 * in `X-User-Id` for ever, and the gateway kept answering for a ghost — its own
 * feed, its own history, all keyed to rows that no longer exist. Nothing on
 * screen would have said so.
 *
 * The server cannot fix this: `localStorage` is per device, and the delete
 * happens on somebody else's. So the device repairs itself the next time it
 * loads, by comparing what it holds against the list it just fetched.
 */
describe('a profile that has been deleted elsewhere', () => {
  it('stops being this browser and asks again', async () => {
    household = [
      { id: 'u_luc', name: 'Luc' },
      { id: 'u_tunkhanh', name: 'Tuấn Khanh' },
    ]
    setCurrentProfileID('u_gone')
    renderGate()

    // The picker, not the app: this browser no longer knows who it is.
    expect(await screen.findByText("Who's watching?")).toBeInTheDocument()
    await waitFor(() =>
      expect(window.localStorage.getItem('yt-profile-id-v1')).toBeNull(),
    )
  })

  it('leaves a valid choice alone', async () => {
    household = [
      { id: 'u_luc', name: 'Luc' },
      { id: 'u_tunkhanh', name: 'Tuấn Khanh' },
    ]
    setCurrentProfileID('u_tunkhanh')
    renderGate()

    expect(await screen.findByText('the app')).toBeInTheDocument()
    expect(window.localStorage.getItem('yt-profile-id-v1')).toBe('u_tunkhanh')
  })
})
