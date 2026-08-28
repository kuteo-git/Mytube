import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useStreamPrefetch } from './queries'
import type { StreamSources } from '../infrastructure/catalogRepository'

const getStream = vi.fn<() => Promise<StreamSources>>()

vi.mock('../infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getStream: () => getStream(),
  },
}))

/**
 * A hover must not answer the question that pressing play asks.
 *
 * `?prefetch=1` deliberately fetches no captions and queues no transfer —
 * hovering a card is not choosing a video. The prefetch used to write its
 * answer under the player's own query key with a five-minute staleTime, so
 * hovering a card and then opening it meant the real request was never issued,
 * and with it neither the caption fetch nor the download.
 *
 * Measured on 2JajSt59wqc: every /stream request the gateway ever saw for that
 * video carried prefetch=true, and its folder was never created at all.
 */
describe('useStreamPrefetch', () => {
  beforeEach(() => {
    getStream.mockReset()
    getStream.mockResolvedValue({})
  })

  it('leaves the player its own key to fill', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )

    const { result } = renderHook(() => useStreamPrefetch(), { wrapper })
    result.current.prefetch('vid1')
    await waitFor(() =>
      expect(client.getQueryData(['stream', 'vid1', 'prefetch'])).toBeDefined(),
    )
    // The one that matters: pressing play still has a question to ask.
    expect(client.getQueryData(['stream', 'vid1'])).toBeUndefined()
  })
})
