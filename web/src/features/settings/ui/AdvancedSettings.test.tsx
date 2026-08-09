import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { RankingSettings } from '@/features/settings/domain/ranking'
import { AdvancedSettings } from './AdvancedSettings'

const getRanking = vi.fn(async () => ({}) as RankingSettings)
const saveRanking = vi.fn(async (s: RankingSettings) => s)

vi.mock('@/features/settings/infrastructure/settingsRepository', () => ({
  settingsRepository: {
    getRanking: () => getRanking(),
    saveRanking: (s: RankingSettings) => saveRanking(s),
  },
}))

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <AdvancedSettings />
    </QueryClientProvider>,
  )
}

function slider(name: RegExp): HTMLInputElement {
  return screen.getByRole('slider', { name }) as HTMLInputElement
}

beforeEach(() => {
  getRanking.mockReset()
  getRanking.mockImplementation(async () => ({}))
  saveRanking.mockReset()
  saveRanking.mockImplementation(async (s: RankingSettings) => s)
})

describe('AdvancedSettings', () => {
  it('shows the built-in value for a setting nobody has touched', async () => {
    renderSection()
    await waitFor(() => expect(slider(/Follow what you are watching now/)).toBeTruthy())

    expect(slider(/Follow what you are watching now/).value).toBe('0.5')
    expect(screen.getByText(/Built in: 50% this sitting/)).toBeTruthy()
  })

  it('reads back what was saved', async () => {
    getRanking.mockImplementation(async () => ({ maxPublishedAgeDays: 30 }))
    renderSection()

    await waitFor(() =>
      expect(slider(/Oldest video the home page will show/).value).toBe('30'),
    )
  })

  // The whole reason the settings are pointers rather than numbers. A blend of
  // zero says "ignore this sitting"; an absent one says "whatever the ranker
  // thinks". Collapsing them would make the first impossible to express.
  it('can set a value to zero without it reading as unset', async () => {
    renderSection()
    await waitFor(() => expect(slider(/Follow what you are watching now/)).toBeTruthy())

    fireEvent.change(slider(/Follow what you are watching now/), { target: { value: '0' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(saveRanking).toHaveBeenCalled())
    expect(saveRanking.mock.calls[0][0].sessionBlend).toBe(0)
  })

  // Clearing writes nothing rather than today's number, so the setting keeps
  // following the ranker if the ranker ever changes.
  it('clears a setting rather than pinning it to the current default', async () => {
    getRanking.mockImplementation(async () => ({ softmaxTemperature: 2 }))
    renderSection()
    await waitFor(() => expect(slider(/How closely the order follows the score/).value).toBe('2'))

    fireEvent.click(screen.getAllByRole('button', { name: 'use built-in' })[0])
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(saveRanking).toHaveBeenCalled())
    expect('softmaxTemperature' in saveRanking.mock.calls[0][0]).toBe(false)
  })

  it('will not save until something has changed', async () => {
    renderSection()
    await waitFor(() => expect(slider(/Follow what you are watching now/)).toBeTruthy())

    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(
      true,
    )
  })

  // Two of these are guard rails rather than tastes, and both can rebuild a
  // measured failure. Saying so is the difference between a knob and a trap.
  it('marks the settings that can break the ordering', async () => {
    renderSection()
    await waitFor(() => expect(slider(/How many videos enter the draw/)).toBeTruthy())

    expect(screen.getAllByText('careful').length).toBe(2)
  })

  // The floor matters: at zero the feed freezes into one order and looks the
  // same on every visit, which is the bug the sampling was added to fix.
  it('does not let the sampling temperature reach zero', async () => {
    renderSection()
    await waitFor(() => expect(slider(/How closely the order follows the score/)).toBeTruthy())

    expect(Number(slider(/How closely the order follows the score/).min)).toBeGreaterThan(0)
  })

  it('says the read failed instead of loading for ever', async () => {
    getRanking.mockImplementation(async () => {
      throw new Error('older gateway')
    })
    renderSection()

    await waitFor(() => expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy())
  })
})
