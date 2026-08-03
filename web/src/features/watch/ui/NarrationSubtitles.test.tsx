import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const cues = [{ start: 0, end: 2, text: 'hello' }]
let translations: Record<string, string> = {}

vi.mock('@/features/watch/application/narration', () => ({
  narrationCues: () => cues,
  currentCueText: (list: typeof cues, time: number) =>
    list.find((c) => time >= c.start && time < c.end)?.text ?? null,
  translatedCue: (text: string) => translations[text],
}))

const { NarrationSubtitles } = await import('./NarrationSubtitles')

const videoAt = (t: number) => ({ currentTime: t }) as HTMLVideoElement

beforeEach(() => {
  vi.useFakeTimers()
  translations = {}
})
afterEach(() => {
  vi.useRealTimers()
  cleanup()
})

/** Advance past one poll of the component's interval. */
const tick = async () => {
  await act(async () => {
    vi.advanceTimersByTime(300)
  })
}

describe('NarrationSubtitles', () => {
  it('draws the translated line while its cue is playing', async () => {
    translations = { hello: 'xin chào' }
    render(<NarrationSubtitles front={() => videoAt(1)} active />)
    await tick()
    expect(screen.getByText('xin chào')).toBeInTheDocument()
  })

  it('draws nothing when the pass has not translated the cue yet', async () => {
    // Falling back to the English would be showing the wrong language under a
    // setting that promised Vietnamese.
    render(<NarrationSubtitles front={() => videoAt(1)} active />)
    await tick()
    expect(screen.queryByText('hello')).not.toBeInTheDocument()
  })

  it('draws nothing between cues', async () => {
    translations = { hello: 'xin chào' }
    render(<NarrationSubtitles front={() => videoAt(5)} active />)
    await tick()
    expect(screen.queryByText('xin chào')).not.toBeInTheDocument()
  })

  it('draws nothing when the output mode does not show subtitles', async () => {
    translations = { hello: 'xin chào' }
    render(<NarrationSubtitles front={() => videoAt(1)} active={false} />)
    await tick()
    expect(screen.queryByText('xin chào')).not.toBeInTheDocument()
  })

  it('reads the front element on every tick, not the one captured at mount', async () => {
    // The player swaps between two <video> layers. A captured element would go
    // on reporting the currentTime of the layer no longer on screen.
    translations = { hello: 'xin chào' }
    let time = 5
    render(<NarrationSubtitles front={() => videoAt(time)} active />)
    await tick()
    expect(screen.queryByText('xin chào')).not.toBeInTheDocument()

    time = 1
    await tick()
    expect(screen.getByText('xin chào')).toBeInTheDocument()
  })
})
