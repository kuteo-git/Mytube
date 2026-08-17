import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ActivityPage } from './ActivityPage'
import type { IngestJob, ScanStatus } from '@/features/catalog/infrastructure/catalogRepository'

const listJobs = vi.fn()
const listScans = vi.fn()
const dismissJob = vi.fn()
const retryJob = vi.fn()
const cancelJob = vi.fn()

vi.mock('@/features/catalog/infrastructure/catalogRepository', () => ({
  httpCatalogRepository: {
    listJobs: (activeOnly: boolean, options?: unknown) => listJobs(activeOnly, options),
    listScans: (limit: number, offset: number) => listScans(limit, offset),
    dismissJob: (id: string) => dismissJob(id),
    retryJob: (id: string) => retryJob(id),
    cancelJob: (id: string) => cancelJob(id),
    refreshTopics: async () => ({}),
  },
}))

function job(over: Partial<IngestJob> = {}): IngestJob {
  return {
    id: 'j1',
    sourceUrl: 'https://www.youtube.com/watch?v=a',
    videoId: 'a',
    title: 'A video',
    state: 'SUCCEEDED',
    progress: 1,
    downloadedBytes: 0,
    totalBytes: 0,
    errorMessage: '',
    attempts: 1,
    createdAt: new Date().toISOString(),
    finishedAt: new Date().toISOString(),
    ...over,
  } as IngestJob
}

function scan(startedAt: string): ScanStatus {
  return {
    startedAt,
    durationMs: 8000,
    sourcesScanned: 63,
    sourcesFailed: 0,
    videosSeen: 400,
    videosAdded: 4,
    errors: [],
    // Every scan built here is one that has already finished — the list this
    // page shows is history.
    running: false,
  }
}

function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ActivityPage />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  listJobs.mockResolvedValue([])
  listScans.mockResolvedValue({ scans: [], total: 0 })
  dismissJob.mockResolvedValue(undefined)
  retryJob.mockResolvedValue(job({ id: 'j2', state: 'QUEUED' }))
  cancelJob.mockResolvedValue(undefined)
})

describe('ActivityPage', () => {
  it('asks for its own view of the job list', async () => {
    // Hiding dismissed jobs is this page's business and nobody else's: the
    // player reads the same list to learn its download has landed.
    renderPage()
    await waitFor(() => expect(listJobs).toHaveBeenCalled())

    expect(listJobs).toHaveBeenCalledWith(false, expect.objectContaining({ hideDismissed: true }))
  })

  it('shows the scans that have run, not just the last one', async () => {
    listScans.mockResolvedValue({
      scans: [scan('2026-08-04T10:00:00Z'), scan('2026-08-04T09:00:00Z')],
      total: 2,
    })
    renderPage()

    await waitFor(() => expect(screen.getAllByText(/63 sources/).length).toBe(2))
  })

  it('offers more scans only while there are more', async () => {
    const first = Array.from({ length: 10 }, (_, i) =>
      scan(`2026-08-04T${String(10 - i).padStart(2, '0')}:00:00Z`),
    )
    listScans.mockResolvedValue({ scans: first, total: 34 })
    renderPage()

    await waitFor(() => expect(screen.getByRole('button', { name: /View more \(24\)/ })).toBeTruthy())
  })

  it('shows ten downloads and reveals the rest on request', async () => {
    listJobs.mockResolvedValue(
      Array.from({ length: 24 }, (_, i) => job({ id: `j${i}`, title: `Video ${i}` })),
    )
    renderPage()

    await waitFor(() => expect(screen.getByText('Video 0')).toBeTruthy())
    expect(screen.queryByText('Video 10')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: /View more \(14\)/ }))
    expect(screen.getByText('Video 10')).toBeTruthy()
    expect(screen.queryByText('Video 20')).toBeNull()
  })

  it('dismisses a completed download', async () => {
    listJobs.mockResolvedValue([job({ id: 'done1', title: 'Finished' })])
    renderPage()
    await waitFor(() => expect(screen.getByText('Finished')).toBeTruthy())

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    await waitFor(() => expect(dismissJob).toHaveBeenCalledWith('done1'))
  })

  it('offers a failed download both a retry and a dismissal', async () => {
    // Retry matters because most failures here are temporary. Without it,
    // hiding would be the only thing anybody could do with one.
    listJobs.mockResolvedValue([
      job({ id: 'bad1', state: 'FAILED', title: 'Broken', errorMessage: 'HTTP Error 429' }),
    ])
    renderPage()
    await waitFor(() => expect(screen.getByText('Broken')).toBeTruthy())

    expect(screen.getByText('HTTP Error 429')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: 'Retry download' }))
    await waitFor(() => expect(retryJob).toHaveBeenCalledWith('bad1'))

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
    await waitFor(() => expect(dismissJob).toHaveBeenCalledWith('bad1'))
  })

  it('cancels rather than hides a download still running', async () => {
    // The same button in the same place, and this is the difference that
    // matters: work carrying on out of sight is what it must never cause.
    listJobs.mockResolvedValue([job({ id: 'run1', state: 'RUNNING', title: 'Running', progress: 0.4 })])
    renderPage()
    await waitFor(() => expect(screen.getByText('Running')).toBeTruthy())

    expect(screen.queryByRole('button', { name: 'Dismiss' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel download' }))
    await waitFor(() => expect(cancelJob).toHaveBeenCalledWith('run1'))
    expect(dismissJob).not.toHaveBeenCalled()
  })

  // There is one worker slot, so a second video pressed at the same time waits
  // its turn rather than transferring alongside the first. Both rows used to
  // read "0%" under a spinning loader — one because it had just started and one
  // because it had not started at all — which is how two videos appeared to be
  // downloading at once on a system that downloads one at a time. A spinner
  // over a percentage is a claim that bytes are moving.
  it('tells a transfer apart from a video waiting its turn', async () => {
    listJobs.mockResolvedValue([
      job({ id: 'run1', state: 'RUNNING', title: 'Transferring', progress: 0 }),
      job({ id: 'wait1', state: 'QUEUED', title: 'Second in line', progress: 0 }),
    ])
    renderPage()

    await waitFor(() => expect(screen.getByText('Second in line')).toBeTruthy())

    // The one actually moving bytes is the only one that may claim a share of
    // the file, and it is the only one counted as a download.
    expect(screen.getAllByText('0%')).toHaveLength(1)
    expect(screen.getByText('Waiting its turn')).toBeTruthy()
  })

  it('says so when nothing has been downloaded', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByText(/Nothing has been downloaded yet/)).toBeTruthy())
  })
})
