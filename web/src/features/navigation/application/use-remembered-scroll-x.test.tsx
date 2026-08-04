import { act, render, within } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { useRememberedScrollX } from './use-remembered-scroll-x'

/** A row that scrolls sideways, with scrollLeft backed by a real number. */
function Row({ storageKey = 'k' }: { storageKey?: string }) {
  const { attach } = useRememberedScrollX(storageKey)
  return (
    <div
      data-testid="row"
      ref={(el) => {
        if (el && !Object.getOwnPropertyDescriptor(el, 'scrollLeft')) {
          let x = 0
          Object.defineProperty(el, 'scrollLeft', {
            get: () => x,
            set: (v: number) => {
              x = v
            },
            configurable: true,
          })
        }
        attach(el)
      }}
    />
  )
}

beforeEach(() => window.sessionStorage.clear())

function scrollRow(el: HTMLElement, x: number) {
  act(() => {
    el.scrollLeft = x
    el.dispatchEvent(new Event('scroll'))
  })
}

describe('a sideways row that remembers where it was', () => {
  it('records the position as it is scrolled', () => {
    const { getByTestId } = render(<Row />)
    scrollRow(getByTestId('row'), 240)
    expect(window.sessionStorage.getItem('k')).toBe('240')
  })

  it('comes back where it was after the page is rebuilt', () => {
    // The fault: switching tabs unmounts Home, so the topic chips returned at
    // the beginning — losing exactly the chip you had scrolled across to find.
    const first = render(<Row />)
    scrollRow(first.getByTestId('row'), 240)
    first.unmount()

    const second = render(<Row />)
    expect(second.getByTestId('row').scrollLeft).toBe(240)
  })

  it('is in place before the first paint', () => {
    // Restored in the callback ref, which runs during commit. In an effect it
    // would paint one frame at the beginning and then jump.
    window.sessionStorage.setItem('k', '180')
    const { getByTestId } = render(<Row />)
    expect(getByTestId('row').scrollLeft).toBe(180)
  })

  it('keeps separate rows apart', () => {
    // Queried through each render's own container: two rows on the page at once
    // is the case being tested, so a document-wide lookup would find both.
    const a = render(<Row storageKey="chips" />)
    scrollRow(within(a.container).getByTestId('row'), 100)
    const b = render(<Row storageKey="queue" />)
    scrollRow(within(b.container).getByTestId('row'), 300)

    expect(window.sessionStorage.getItem('chips')).toBe('100')
    expect(window.sessionStorage.getItem('queue')).toBe('300')
  })

  it('starts at the beginning when nothing was stored', () => {
    const { getByTestId } = render(<Row />)
    expect(getByTestId('row').scrollLeft).toBe(0)
  })

  it('leaves nothing listening on an element it has let go of', () => {
    // A callback ref fires with null on unmount, and in development React fires
    // it twice on mount. Assignment replaces rather than accumulates, so the
    // element cannot end up reporting one scroll several times.
    const { getByTestId, unmount } = render(<Row />)
    const el = getByTestId('row')
    unmount()
    expect(el.onscroll).toBeNull()
  })
})
