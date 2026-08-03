import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  BATCH_SIZE,
  FIRST_BATCH,
  planBatches,
  translateBatch,
} from './narration-batch'

afterEach(() => vi.unstubAllGlobals())

describe('planBatches', () => {
  it('keeps the first batch short so the first word arrives quickly', () => {
    const plan = planBatches(40)
    expect(plan[0]).toEqual({ start: 0, end: FIRST_BATCH })
    expect(plan[1]).toEqual({ start: FIRST_BATCH, end: FIRST_BATCH + BATCH_SIZE })
  })

  it('covers every cue exactly once with no gap', () => {
    const plan = planBatches(40)
    expect(plan[0].start).toBe(0)
    expect(plan[plan.length - 1].end).toBe(40)
    for (let i = 1; i < plan.length; i++) {
      expect(plan[i].start).toBe(plan[i - 1].end)
    }
  })

  it('handles a video shorter than one batch', () => {
    expect(planBatches(2)).toEqual([{ start: 0, end: 2 }])
  })

  it('returns nothing for no cues', () => {
    expect(planBatches(0)).toEqual([])
  })
})

describe('translateBatch', () => {
  it('returns the server translations', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ translations: ['một', 'hai'] }),
      }),
    )
    expect(await translateBatch(['one', 'two'], [])).toEqual([
      'một',
      'hai',
    ])
  })

  it('pads a short answer rather than shifting cues', async () => {
    // Belt and braces: the server refuses misaligned batches, but a length
    // mismatch here would speak every later cue at the wrong moment.
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ translations: ['một'] }),
      }),
    )
    expect(await translateBatch(['one', 'two'], [])).toEqual(['một', ''])
  })

  it('resolves to blanks when the request fails', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('down')))
    expect(await translateBatch(['one', 'two'], [])).toEqual(['', ''])
  })
})
