import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { FeedMixSettings } from './FeedMixSettings'

const defaults = {
  subscribedPercent: 25,
  affinityPercent: 60,
  discoveryPercent: 15,
}

const fixedShares = { continueWatching: 10, rewatch: 8, freshSubscribed: 10 }

const getFeedMix = vi.fn(async () => ({ ...defaults, defaults, fixedShares }))
const saveFeedMix = vi.fn(async (mix: unknown) => mix)
const getBucketSizes = vi.fn(async () => ({}) as Record<string, number>)

vi.mock('@/features/settings/infrastructure/settingsRepository', () => ({
  settingsRepository: {
    getFeedMix: () => getFeedMix(),
    saveFeedMix: (mix: unknown) => saveFeedMix(mix),
    getBucketSizes: () => getBucketSizes(),
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
  // Implementations, not just call counts: a test that makes one of these throw
  // would otherwise hand the failure to whichever test ran next.
  getFeedMix.mockReset()
  getFeedMix.mockImplementation(async () => ({ ...defaults, defaults, fixedShares }))
  saveFeedMix.mockReset()
  saveFeedMix.mockImplementation(async (mix: unknown) => mix)
  getBucketSizes.mockReset()
  getBucketSizes.mockImplementation(async () => ({}))
})

describe('FeedMixSettings', () => {
  it('shows what is saved, and what each share means on a page', async () => {
    renderSection()
    await waitFor(() => expect(slider(/Channels you follow/)).toBeTruthy())

    expect(slider(/Channels you follow/).value).toBe('25')
    expect(slider(/More of what you watch/).value).toBe('60')
    expect(slider(/Something new/).value).toBe('15')
    // A percentage of a page nobody should have to work out by hand.
    // 60% of the 72% the three sliders divide, over a window of 24.
    expect(screen.getAllByText(/60% · 10 of 24/).length).toBeGreaterThan(0)
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

  it('says the read failed instead of loading for ever', async () => {
    // What this looked like in the wild: the gateway was up but running a build
    // from before the endpoint existed, so the request 404'd and the section
    // sat on "Loading…" with nothing to press.
    getFeedMix.mockImplementation(async () => {
      throw new Error('404 page not found')
    })
    renderSection()

    await waitFor(() => expect(screen.getByText(/Could not read the current mix/)).toBeTruthy())
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy()
    expect(screen.queryByText('Loading…')).toBeNull()
  })

  it('explains the share it does not divide', async () => {
    // Eighteen per cent of the page is never up for division, and a viewer
    // counting videos on a page has no way to discover that on their own.
    renderSection()
    await waitFor(() => expect(screen.getByText('Defaults')).toBeTruthy())

    expect(screen.getByText(/72% of the page/)).toBeTruthy()
    expect(screen.getByText(/part way through \(10%\)/)).toBeTruthy()
  })
})

describe('what each share has to choose from', () => {
  // The failure this exists to surface: the sliders divide a page, but a share
  // can only be filled from videos that exist. The affinity bucket held
  // twenty-five videos against three and a half thousand subscribed ones, so a
  // 60% share spent half of every page scraping its floor — and nothing on this
  // screen would have told anybody.
  it('warns when a share is larger than the library can fill', async () => {
    getBucketSizes.mockImplementation(async () => ({
      subscribed: 3432,
      affinity: 25,
      discovery: 158,
    }))
    renderSection()

    await waitFor(() =>
      expect(screen.getByText(/Only 25 videos fit this/)).toBeTruthy(),
    )
    // And a bucket with plenty in it is not news, so it is stated plainly.
    expect(screen.getByText(/3432 videos fit this/)).toBeTruthy()
  })

  it('says so plainly when a share has nothing at all', async () => {
    getBucketSizes.mockImplementation(async () => ({ discovery: 0 }))
    renderSection()

    await waitFor(() =>
      expect(screen.getByText(/Nothing in your library fits this/)).toBeTruthy(),
    )
  })

  // The count costs a full ranking pass. Losing it must cost a line of context,
  // never the setting.
  it('still works when the count cannot be fetched', async () => {
    getBucketSizes.mockImplementation(async () => {
      throw new Error('recsys is down')
    })
    renderSection()

    await waitFor(() => expect(slider(/Channels you follow/)).toBeTruthy())
    expect(slider(/Channels you follow/).value).toBe('25')
  })
})
