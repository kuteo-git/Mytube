import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { SearchResultsPage } from '@/pages/SearchResultsPage'

/**
 * A pasted channel address opens the channel.
 *
 * It used to go to search, which is the wrong verb twice over: `ytsearch20:` is
 * asked to *look for* the text of an address, spending one counted upstream
 * request (CLAUDE.md §8 risk 5) to find something whose location was already
 * stated — and the library half of the page is full-text over titles, which an
 * address never matches. The page then said "Channel and playlist links cannot
 * be opened here yet", which was true and was the whole of what happened.
 *
 * Which channel an address names is decided at the gateway, not here. This only
 * tests what the page does with the answer.
 */

let resolved: string | null = null

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    resolveChannel: vi.fn(async () => resolved),
    search: vi.fn(async () => ({ videos: [], nextPageToken: '' })),
    discover: vi.fn(async () => []),
  },
}))

beforeEach(() => {
  resolved = null
})

function renderSearch(query: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[`/search?q=${encodeURIComponent(query)}`]}>
        <Routes>
          <Route path="/search" element={<SearchResultsPage />} />
          <Route path="/channel/:channelId" element={<div>channel page</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('a pasted channel address', () => {
  it('opens the channel instead of searching for the text of the URL', async () => {
    resolved = 'UCRU9vwwARx8jEACqNpi_KFA'
    renderSearch('https://youtube.com/@champsnetwork?si=GXm_j8ad-WG3cCym')

    // The address that started this. It carries the share parameter the
    // YouTube app appends, which is exactly how a link arrives in practice.
    expect(await screen.findByText('channel page')).toBeInTheDocument()
  })

  it('leaves an ordinary search where it is', async () => {
    resolved = null
    renderSearch('how to read like a pro')

    // The page's own title, not one of the section headings below it.
    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('how to read like a pro'),
    )
    expect(screen.queryByText('channel page')).toBeNull()
  })
})
