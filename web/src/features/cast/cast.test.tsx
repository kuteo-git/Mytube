import { QueryClient, QueryClientProvider, focusManager } from '@tanstack/react-query'
import { act, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ToastProvider } from '@/shared/ui/toast'
import { CastProvider, useCast } from './application/cast-context'
import { IDLE_POLL_MS, REMOTE_POLL_MS, useCastSessions, useStartCast } from './application/queries'
import { indexAfterEdit, moveItem, type CastSession } from './domain/cast'
import { CastError } from './infrastructure/cast-repository'
import { CastButton } from './ui/CastButton'
import { CastRemote } from './ui/CastRemote'

const repo = vi.hoisted(() => ({
  devices: vi.fn(),
  sessions: vi.fn(),
  session: vi.fn(),
  start: vi.fn(),
  pause: vi.fn(),
  resume: vi.fn(),
  next: vi.fn(),
  stop: vi.fn(),
  seek: vi.fn(),
  volume: vi.fn(),
  jump: vi.fn(),
  replaceQueue: vi.fn(),
  handoff: vi.fn(),
}))

vi.mock('./infrastructure/cast-repository', async (importOriginal) => ({
  ...(await importOriginal<typeof import('./infrastructure/cast-repository')>()),
  castRepository: repo,
}))

function session(over: Partial<CastSession> = {}): CastSession {
  return {
    id: 's1',
    device: { id: 'r1-living', name: 'Loa R1', kind: 'r1' },
    state: 'playing',
    endedReason: null,
    queue: [
      { videoId: 'a', title: 'Song A', channel: 'Chan', thumbnailUrl: '', durationSeconds: 200 },
      { videoId: 'b', title: 'Song B', channel: 'Chan', thumbnailUrl: '', durationSeconds: 180 },
      { videoId: 'c', title: 'Song C', channel: 'Chan', thumbnailUrl: '', durationSeconds: 150 },
    ],
    currentIndex: 0,
    positionSeconds: 30,
    durationSeconds: 200,
    volume: 0,
    version: 4,
    startedBy: 'u1',
    ...over,
  }
}

const R1 = { id: 'r1-living', name: 'Loa R1', kind: 'r1' as const, model: 'R1' }

let client: QueryClient
function Providers({ children, path = '/' }: { children: ReactNode; path?: string }) {
  return (
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        <ToastProvider>
          <CastProvider>
            <Routes>
              <Route path="/watch/:id" element={<p>watch page</p>} />
              <Route path="*" element={children} />
            </Routes>
          </CastProvider>
        </ToastProvider>
      </MemoryRouter>
    </QueryClientProvider>
  )
}

beforeEach(() => {
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  for (const fn of Object.values(repo)) fn.mockReset()
  repo.devices.mockResolvedValue([R1])
  repo.sessions.mockResolvedValue([])
  repo.session.mockResolvedValue(session())
})

afterEach(() => {
  vi.useRealTimers()
  focusManager.setFocused(undefined)
})

describe('the cast button', () => {
  it('is not drawn when no speaker answers', async () => {
    repo.devices.mockResolvedValue([])
    render(<CastButton className="" onPick={() => {}} />, { wrapper: Providers })
    await waitFor(() => expect(repo.devices).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: 'Cast to a speaker' })).not.toBeInTheDocument()
  })

  it('is drawn when one does, and picking a speaker hands it over', async () => {
    const onPick = vi.fn()
    render(<CastButton className="" onPick={onPick} />, { wrapper: Providers })
    fireEvent.click(await screen.findByRole('button', { name: 'Cast to a speaker' }))
    fireEvent.click(await screen.findByRole('button', { name: /Loa R1/ }))
    expect(onPick).toHaveBeenCalledWith(R1)
  })
})

describe('starting a cast', () => {
  it('sends the whole seconds the viewer was at and the items in order, then opens the remote', async () => {
    repo.start.mockResolvedValue(session())
    const { result } = renderHook(() => ({ start: useStartCast(), cast: useCast() }), { wrapper: Providers })
    act(() => result.current.start.mutate({ device: R1, videoIds: ['a', 'b'], startPositionSeconds: 89.6 }))
    await waitFor(() => expect(result.current.cast.remoteSessionId).toBe('s1'))
    expect(repo.start).toHaveBeenCalledWith({ deviceId: 'r1-living', videoIds: ['a', 'b'], startIndex: 0, startPositionSeconds: 90 })
  })
})

describe('polling', () => {
  it('asks every second while the remote is open, and stops in a hidden tab', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const Open = () => {
      const { openRemote } = useCast()
      return (
        <>
          <button onClick={() => openRemote('s1')}>open</button>
          <CastRemote />
        </>
      )
    }
    render(<Open />, { wrapper: Providers })
    fireEvent.click(screen.getByText('open'))
    await screen.findByRole('dialog')
    const before = repo.session.mock.calls.length
    await act(() => vi.advanceTimersByTimeAsync(REMOTE_POLL_MS * 3 + 50))
    expect(repo.session.mock.calls.length - before).toBeGreaterThanOrEqual(3)

    focusManager.setFocused(false)
    const hidden = repo.session.mock.calls.length
    await act(() => vi.advanceTimersByTimeAsync(REMOTE_POLL_MS * 5))
    expect(repo.session.mock.calls.length).toBe(hidden)
  })

  it('asks about running sessions every five seconds otherwise', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    renderHook(() => useCastSessions(), { wrapper: Providers })
    await waitFor(() => expect(repo.sessions).toHaveBeenCalledTimes(1))
    await act(() => vi.advanceTimersByTimeAsync(IDLE_POLL_MS + 50))
    expect(repo.sessions).toHaveBeenCalledTimes(2)
    await act(() => vi.advanceTimersByTimeAsync(IDLE_POLL_MS - 500))
    expect(repo.sessions).toHaveBeenCalledTimes(2)
  })
})

function openRemote(extra?: ReactNode) {
  const Open = () => {
    const { openRemote: open, remoteSessionId } = useCast()
    return (
      <>
        {extra}
        <button onClick={() => open('s1')}>open</button>
        <span data-testid="remote">{remoteSessionId ?? 'closed'}</span>
        <CastRemote />
      </>
    )
  }
  render(<Open />, { wrapper: Providers })
  fireEvent.click(screen.getByText('open'))
}

describe('the remote', () => {
  it('says the speaker was taken and closes', async () => {
    repo.session.mockResolvedValue(session({ state: 'ended', endedReason: 'taken_over' }))
    openRemote()
    expect(await screen.findByText('The speaker switched to another source.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('remote')).toHaveTextContent('closed'))
  })

  it('closes without a word when the viewer stopped it', async () => {
    repo.session.mockResolvedValue(session({ state: 'ended', endedReason: 'stopped' }))
    openRemote()
    await waitFor(() => expect(screen.getByTestId('remote')).toHaveTextContent('closed'))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('pauses and seeks the speaker', async () => {
    repo.pause.mockResolvedValue(session({ state: 'paused' }))
    repo.seek.mockResolvedValue(session({ positionSeconds: 120 }))
    openRemote()
    fireEvent.click(await screen.findByRole('button', { name: 'Pause' }))
    await waitFor(() => expect(repo.pause).toHaveBeenCalledWith('s1'))

    const bar = screen.getByRole('slider', { name: 'Position' })
    fireEvent.change(bar, { target: { value: '120' } })
    expect(repo.seek).not.toHaveBeenCalled()
    fireEvent.pointerUp(bar)
    await waitFor(() => expect(repo.seek).toHaveBeenCalledWith('s1', 120))
  })

  it('moves a song up keeping the one playing where it is in the queue', async () => {
    repo.session.mockResolvedValue(session({ currentIndex: 1 }))
    repo.replaceQueue.mockResolvedValue(session())
    openRemote()
    fireEvent.click(await screen.findByRole('button', { name: 'Move Song C up' }))
    // a, [b], c -> a, c, [b]: the song playing is now third.
    await waitFor(() => expect(repo.replaceQueue).toHaveBeenCalledWith('s1', 4, ['a', 'c', 'b'], 2))
  })

  it('reloads and says so when somebody else edited the queue first', async () => {
    repo.replaceQueue.mockRejectedValue(new CastError('stale_version', 409))
    openRemote()
    const invalidate = vi.spyOn(client, 'invalidateQueries')
    fireEvent.click(await screen.findByRole('button', { name: 'Remove Song B' }))
    expect(await screen.findByText('Someone else changed the queue. It has been reloaded.')).toBeInTheDocument()
    // Asked for at once, not left to the next poll.
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['cast', 'session', 's1'] })
  })

  it('hands back to the player in place when it holds the video', async () => {
    repo.handoff.mockResolvedValue({ videoId: 'a', positionSeconds: 130 })
    const resume = vi.fn(() => true)
    const Register = () => {
      useCast().setLocalResume(resume)
      return null
    }
    openRemote(<Register />)
    fireEvent.click(await screen.findByRole('button', { name: 'Stop casting' }))
    await waitFor(() => expect(resume).toHaveBeenCalledWith('a', 130))
    expect(screen.queryByText('watch page')).not.toBeInTheDocument()
  })

  it('opens the watch page when the player holds something else', async () => {
    repo.handoff.mockResolvedValue({ videoId: 'zz', positionSeconds: 130 })
    openRemote()
    fireEvent.click(await screen.findByRole('button', { name: 'Stop casting' }))
    expect(await screen.findByText('watch page')).toBeInTheDocument()
  })
})

describe('queue edits', () => {
  it('moves an item', () => {
    expect(moveItem(['a', 'b', 'c'], 2, 0)).toEqual(['c', 'a', 'b'])
    expect(moveItem(['a', 'b'], 5, 0)).toEqual(['a', 'b'])
  })

  it('keeps the current song when it survives the edit, and the place when it does not', () => {
    const s = session({ currentIndex: 1 })
    expect(indexAfterEdit(s, [s.queue[2], s.queue[1]])).toBe(1)
    expect(indexAfterEdit(s, [s.queue[0], s.queue[2]])).toBe(1)
    expect(indexAfterEdit(s, [s.queue[0]])).toBe(0)
  })
})
