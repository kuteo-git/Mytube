import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  BATCH_SIZE,
  FIRST_BATCH,
  lastBatchError,
  planBatches,
  translateBatch,
  workOrder,
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
    expect(lastBatchError()).not.toBe('')
  })

  it('does not call leaving the video a failure', async () => {
    // Closing a video aborts the batch in flight. That is the viewer doing
    // something ordinary, not the translator breaking, and recording it would
    // put an error on the status line of the next video they open.
    const ctrl = new AbortController()
    ctrl.abort()
    vi.stubGlobal(
      'fetch',
      vi.fn().mockRejectedValue(new DOMException('aborted', 'AbortError')),
    )
    expect(await translateBatch(['one'], [], ctrl.signal)).toEqual([''])
    expect(lastBatchError()).toBe('')
  })

  it('passes the abort signal to the request', async () => {
    // Without it, leaving a video leaves the router working on a batch nobody
    // will ever read — the same waste that cancelling a download avoids.
    const ctrl = new AbortController()
    const f = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ translations: ['một'] }),
    })
    vi.stubGlobal('fetch', f)
    await translateBatch(['one'], [], ctrl.signal)
    expect(f.mock.calls[0][1].signal).toBe(ctrl.signal)
  })
})

describe('workOrder', () => {
  it('starts at the playhead and wraps to cover the beginning', () => {
    // Everything gets translated eventually — what changes is the order. The
    // viewer's next line comes first; the part before them is still owed, both
    // for a backward seek and for the subtitle file written at the end.
    expect(workOrder(6, 2)).toEqual([2, 3, 4, 5, 0, 1])
  })

  it('is the plain order when starting from the top', () => {
    expect(workOrder(4, 0)).toEqual([0, 1, 2, 3])
  })

  it('covers every index exactly once', () => {
    const order = workOrder(50, 37)
    expect(new Set(order).size).toBe(50)
    expect(order[0]).toBe(37)
  })

  it('has nothing to do with no cues', () => {
    expect(workOrder(0, 0)).toEqual([])
  })
})
