import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { ProfilePicker } from './ProfilePicker'
import { setCurrentProfileID } from '../infrastructure/current-profile'
import type { Profile } from '../domain/profile'

/**
 * Deleting a profile, and the two places the button must not appear.
 *
 * The numbers in the confirmation are the point of it. "Delete profile" sounds
 * far lighter than what it does — on this household's largest member, 351
 * subscriptions, 889 watched videos and 63,908 ranking signals — and a dialog
 * that only asks "are you sure?" is asking about a word rather than about the
 * thing.
 */

let household: Profile[] = []
const usage = vi.fn(async (_id: string) => ({
  subscriptions: 8,
  watched: 4,
  reactions: 41,
  saved: 0,
  watchLater: 6,
  playlists: 0,
  comments: 0,
  signals: 72,
}))
const remove = vi.fn(async (_id: string) => {})

vi.mock('../infrastructure/profileRepository', () => ({
  httpProfileRepository: {
    list: vi.fn(async () => household),
    create: vi.fn(async (name: string) => ({ id: 'u_new', name })),
    usage: (id: string) => usage(id),
    remove: (id: string) => remove(id),
  },
}))

function renderPicker(manage = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <ProfilePicker manage={manage} />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  window.localStorage.clear()
  household = [
    { id: 'u_luc', name: 'Luc' },
    { id: 'u_tunkhanh', name: 'Tuấn Khanh' },
  ]
  setCurrentProfileID('u_luc')
  usage.mockClear()
  remove.mockClear()
})

describe('deleting a profile', () => {
  it('says what it will take before it takes it', async () => {
    renderPicker()

    fireEvent.click(await screen.findByLabelText('Delete Tuấn Khanh'))

    // The real counts, from the same query that then does the deleting. Two
    // queries would be two definitions of "what belongs to this profile",
    // agreeing until the day one of them changes.
    expect(await screen.findByText('8')).toBeInTheDocument()
    expect(screen.getByText('subscriptions')).toBeInTheDocument()
    expect(screen.getByText('41')).toBeInTheDocument()

    // And the zeroes are left out. A list padded with "0 playlists" reads as a
    // form rather than as a warning.
    expect(screen.queryByText('playlists')).toBeNull()

    // The reassurance matters as much as the warning: people hesitate because
    // they think the videos go too.
    expect(screen.getByText(/videos and channels themselves stay/i)).toBeInTheDocument()

    expect(remove).not.toHaveBeenCalled()
  })

  it('deletes only when the second button is pressed', async () => {
    renderPicker()
    fireEvent.click(await screen.findByLabelText('Delete Tuấn Khanh'))
    await screen.findByText('subscriptions')

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Delete' }))
    })

    await waitFor(() => expect(remove).toHaveBeenCalledWith('u_tunkhanh'))
  })

  /**
   * Deleting the profile you are using would leave this browser holding the id
   * of somebody who no longer exists. The gateway refuses it, so offering the
   * button would be offering a refusal — the dead control §5 forbids.
   */
  it('is not offered for the profile in use', async () => {
    renderPicker()
    await screen.findByLabelText('Delete Tuấn Khanh')
    expect(screen.queryByLabelText('Delete Luc')).toBeNull()
  })

  /** Nor for the last one: an empty list is a library nobody owns. */
  it('is not offered when only one profile is left', async () => {
    household = [{ id: 'u_luc', name: 'Luc' }]
    renderPicker()

    await waitFor(() => expect(screen.getByText('Luc')).toBeInTheDocument())
    expect(screen.queryByLabelText('Delete Luc')).toBeNull()
  })

  /**
   * The gate shows the same component. Somebody there is trying to start
   * watching, and a row of delete buttons in front of them is both noise and a
   * hazard.
   */
  it('is not offered at all outside the manage screen', async () => {
    renderPicker(false)

    await waitFor(() => expect(screen.getByText('Tuấn Khanh')).toBeInTheDocument())
    expect(screen.queryByLabelText('Delete Tuấn Khanh')).toBeNull()
  })
})
