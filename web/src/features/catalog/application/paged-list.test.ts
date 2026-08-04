import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ACTIVITY_PAGE_SIZE, usePagedList } from './paged-list'

const items = (n: number) => Array.from({ length: n }, (_, i) => i)

describe('usePagedList', () => {
  it('shows one page and reports what is left', () => {
    const { result } = renderHook(() => usePagedList(items(24)))

    expect(result.current.visible).toHaveLength(ACTIVITY_PAGE_SIZE)
    expect(result.current.remaining).toBe(14)
  })

  it('reveals another page at a time', () => {
    const { result } = renderHook(() => usePagedList(items(24)))

    act(() => result.current.showMore())
    expect(result.current.visible).toHaveLength(20)
    expect(result.current.remaining).toBe(4)

    act(() => result.current.showMore())
    expect(result.current.visible).toHaveLength(24)
    expect(result.current.remaining).toBe(0)
  })

  it('has nothing more to offer when the list fits', () => {
    const { result } = renderHook(() => usePagedList(items(3)))

    expect(result.current.visible).toHaveLength(3)
    expect(result.current.remaining).toBe(0)
  })

  it('does not strand the count when the list shrinks under it', () => {
    // Real on this page: a job is dismissed, or a download finishes and moves
    // from one group to another, while the group is expanded.
    const { rerender, result } = renderHook(({ n }) => usePagedList(items(n)), {
      initialProps: { n: 40 },
    })

    act(() => result.current.showMore())
    expect(result.current.visible).toHaveLength(20)

    rerender({ n: 5 })
    expect(result.current.visible).toHaveLength(5)
    expect(result.current.remaining).toBe(0)
  })

  it('never falls below one page', () => {
    const { rerender, result } = renderHook(({ n }) => usePagedList(items(n)), {
      initialProps: { n: 2 },
    })
    rerender({ n: 30 })

    expect(result.current.visible).toHaveLength(ACTIVITY_PAGE_SIZE)
  })
})
