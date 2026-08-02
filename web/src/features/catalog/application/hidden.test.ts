import { beforeEach, describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { hiddenReason, hideVideo, resetHidden, unhideVideo, useHiddenVideos } from './hidden'

beforeEach(() => {
  window.localStorage.clear()
  resetHidden()
})

describe('hideVideo', () => {
  it('remembers why, not only that', () => {
    // "I have seen this" and "stop showing me this" are different statements.
    // Flattening them now would make them impossible to tell apart later.
    hideVideo('a', 'watched')
    hideVideo('b', 'not-interested')

    expect(hiddenReason('a')).toBe('watched')
    expect(hiddenReason('b')).toBe('not-interested')
  })

  it('survives a reload', () => {
    hideVideo('a', 'watched')
    expect(JSON.parse(window.localStorage.getItem('yt-hidden-videos-v2')!)).toEqual({
      a: 'watched',
    })
  })

  it('ignores an empty id', () => {
    hideVideo('', 'watched')
    expect(hiddenReason('')).toBeUndefined()
  })
})

describe('unhideVideo', () => {
  it('puts it back', () => {
    hideVideo('a', 'watched')
    unhideVideo('a')
    expect(hiddenReason('a')).toBeUndefined()
  })
})

describe('telling everyone', () => {
  it('re-renders every reader, not only the list that changed', () => {
    // The old version edited one query's cache, so a card hidden from the feed
    // stayed exactly where it was in every other section on the page.
    const { result } = renderHook(() => useHiddenVideos())
    expect(result.current.has('a')).toBe(false)

    act(() => hideVideo('a', 'watched'))

    expect(result.current.has('a')).toBe(true)
  })

  it('hands back the same snapshot when nothing changed', () => {
    // Returning a fresh Set each time would re-render every consumer on every
    // render of anything.
    const { result, rerender } = renderHook(() => useHiddenVideos())
    const first = result.current
    rerender()
    expect(result.current).toBe(first)
  })
})
