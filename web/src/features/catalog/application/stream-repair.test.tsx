import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { renderHook, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useStream } from './queries'
import type { StreamSources } from '../infrastructure/catalogRepository'

const getStream = vi.fn<() => Promise<StreamSources>>()

vi.mock('../infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    getStream: () => getStream(),
  },
}))

function harness() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const invalidate = vi.spyOn(client, 'invalidateQueries')
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  )
  return { wrapper, invalidate }
}

/** What the gateway answers for a video with no copy on disk. */
const upstreamOnly: StreamSources = {
  instant: { url: 'https://upstream/360', height: 360, seekable: true },
}

beforeEach(() => {
  getStream.mockReset()
})

describe('useStream', () => {
  it('refetches the video when the gateway repaired its row', async () => {
    // The case: the file was deleted by hand, so the gateway marked the video
    // evicted while answering. The page is still holding a copy that says
    // READY, and videoPollInterval stops polling once the state looks settled —
    // so nothing else would ever correct it.
    getStream.mockResolvedValue({ ...upstreamOnly, repaired: true })
    const { wrapper, invalidate } = harness()

    const { result } = renderHook(() => useStream('abc'), { wrapper })
    await waitFor(() => expect(result.current.data).toBeTruthy())

    // Broadened when the video key gained the profile: ['video', me, id] is not
    // matched by a ['video', id] prefix, and a repair that silently stopped
    // refetching would leave the watch page on the row it just replaced.
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['video'] })
  })

  it('leaves the video alone on an ordinary answer', async () => {
    getStream.mockResolvedValue(upstreamOnly)
    const { wrapper, invalidate } = harness()

    const { result } = renderHook(() => useStream('abc'), { wrapper })
    await waitFor(() => expect(result.current.data).toBeTruthy())

    expect(invalidate).not.toHaveBeenCalledWith({ queryKey: ['video'] })
  })
})
