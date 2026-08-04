import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { FeedMixSettings } from './FeedMixSettings'

const defaults = {
  subscribedPercent: 25,
  affinityPercent: 60,
  discoveryPercent: 15,
}

const getFeedMix = vi.fn(async () => ({ ...defaults, defaults }))
const saveFeedMix = vi.fn(async (mix: unknown) => mix)

vi.mock('@/features/settings/infrastructure/settingsRepository', () => ({
  settingsRepository: {
    getFeedMix: () => getFeedMix(),
    saveFeedMix: (mix: unknown) => saveFeedMix(mix),
  },
}))

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <FeedMixSettings />
    </QueryClientProvider>,
  )
}

/** The slider for one of the three shares, found by its label. */
function slider(name: RegExp): HTMLInputElement {
  return screen.getByRole('slider', { name }) as HTMLInputElement
}

beforeEach(() => {
  getFeedMix.mockClear()
  saveFeedMix.mockClear()
  saveFeedMix.mockImplementation(async (mix: unknown) => mix)
})

describe('FeedMixSettings', () => {
  it('shows what is saved, and what each share means on a page', async () => {
    renderSection()
    await waitFor(() => expect(slider(/Channels you follow/)).toBeTruthy())

    expect(slider(/Channels you follow/).value).toBe('25')
    expect(slider(/More of what you watch/).value).toBe('60')
    expect(slider(/Something new/).value).toBe('15')
    // A percentage of a page nobody should have to work out by hand.
    expect(screen.getAllByText(/60% · 12 of 24/).length).toBeGreaterThan(0)
  })

  it('takes from the other two when one is raised', async () => {
    renderSection()
    await waitFor(() => expect(slider(/Something new/)).toBeTruthy())

    fireEvent.change(slider(/Something new/), { target: { value: '50' } })

    const total =
      Number(slider(/Channels you follow/).value) +
      Number(slider(/More of what you watch/).value) +
      Number(slider(/Something new/).value)
    expect(total).toBe(100)
    expect(slider(/Something new/).value).toBe('50')
  })

  it('will not save until something has actually changed', async () => {
    renderSection()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeTruthy())

    expect(screen.getByRole('button', { name: 'Save' })).toHaveProperty('disabled', true)

    fireEvent.change(slider(/Something new/), { target: { value: '40' } })
    expect(screen.getByRole('button', { name: 'Save' })).toHaveProperty('disabled', false)
  })

  it('confirms the save, because nothing on this page shows the result', async () => {
    // The feed is somewhere else. Without a word here, a save that worked and a
    // save that failed look exactly alike.
    renderSection()
    await waitFor(() => expect(slider(/Something new/)).toBeTruthy())

    fireEvent.change(slider(/Something new/), { target: { value: '40' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(screen.getByText(/Saved/)).toBeTruthy())
    expect(saveFeedMix).toHaveBeenCalledWith(
      expect.objectContaining({ discoveryPercent: 40 }),
    )
  })

  it('says so when the save fails', async () => {
    saveFeedMix.mockImplementation(async () => {
      throw new Error('gateway down')
    })
    renderSection()
    await waitFor(() => expect(slider(/Something new/)).toBeTruthy())

    fireEvent.change(slider(/Something new/), { target: { value: '40' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(screen.getByText(/Could not save/)).toBeTruthy())
  })

  it('puts the defaults back', async () => {
    renderSection()
    await waitFor(() => expect(slider(/Something new/)).toBeTruthy())

    fireEvent.change(slider(/Something new/), { target: { value: '90' } })
    fireEvent.click(screen.getByRole('button', { name: /Reset to default/ }))

    expect(slider(/Channels you follow/).value).toBe('25')
    expect(slider(/More of what you watch/).value).toBe('60')
    expect(slider(/Something new/).value).toBe('15')
  })

  it('explains the share it does not divide', async () => {
    // Eighteen per cent of the page is never up for division, and a viewer
    // counting videos on a page has no way to discover that on their own.
    renderSection()
    await waitFor(() => expect(screen.getByText('Defaults')).toBeTruthy())

    expect(screen.getByText(/82% of the page/)).toBeTruthy()
    expect(screen.getByText(/part way through \(10%\)/)).toBeTruthy()
  })
})
