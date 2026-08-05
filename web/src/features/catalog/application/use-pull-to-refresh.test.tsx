import { act, render } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { REFRESH_THRESHOLD } from './pull-to-refresh'
import { usePullToRefresh } from './use-pull-to-refresh'

let scroller: HTMLElement
let scrollTop = 0

/**
 * A touch event jsdom will carry.
 *
 * `TouchEvent` is not constructible here, and the hook reads exactly two things
 * off it — the finger's position and whether the default can be called off — so
 * those are what a stand-in has to provide.
 */
function touch(type: string, ys: number[], cancelable = true) {
  const e = new Event(type, { cancelable, bubbles: true })
  Object.defineProperty(e, 'touches', {
    value: ys.map((clientY) => ({ clientY })),
  })
  return e
}

function Harness({ onRefresh, enabled = true }: { onRefresh: () => Promise<unknown>; enabled?: boolean }) {
  const { distance, refreshing, offset } = usePullToRefresh({ scroller, enabled, onRefresh })
  return (
    <span data-testid="state">
      {distance.toFixed(0)}:{offset.toFixed(0)}:{refreshing ? 'refreshing' : 'idle'}
    </span>
  )
}
const state = () => document.querySelector('[data-testid="state"]')!.textContent!

beforeEach(() => {
  scrollTop = 0
  scroller = document.createElement('div')
  Object.defineProperty(scroller, 'scrollTop', {
    get: () => scrollTop,
    configurable: true,
  })
  document.body.appendChild(scroller)
})

/** One finger, from a point, down to another, and released. */
function pull(from: number, to: number) {
  act(() => {
    scroller.dispatchEvent(touch('touchstart', [from]))
    scroller.dispatchEvent(touch('touchmove', [to]))
    scroller.dispatchEvent(touch('touchend', []))
  })
}

describe('pulling the feed down', () => {
  it('refetches once the pull is long enough', async () => {
    const onRefresh = vi.fn(async () => {})
    render(<Harness onRefresh={onRefresh} />)

    pull(100, 400)

    expect(onRefresh).toHaveBeenCalledOnce()
  })

  it('does nothing for a short pull', () => {
    const onRefresh = vi.fn(async () => {})
    render(<Harness onRefresh={onRefresh} />)

    pull(100, 130)

    expect(onRefresh).not.toHaveBeenCalled()
  })

  it('does not start unless the page is already at the top', () => {
    // One pixel down, the same movement means "scroll back up" — a far more
    // common thing to want, and taking it would make the feed feel stuck.
    scrollTop = 1
    const onRefresh = vi.fn(async () => {})
    render(<Harness onRefresh={onRefresh} />)

    pull(100, 400)

    expect(onRefresh).not.toHaveBeenCalled()
  })

  it('calls off the browser\'s own bounce', () => {
    // Otherwise one finger pulls the page twice: this moves the content while
    // the scroller rubber-bands underneath it.
    render(<Harness onRefresh={async () => {}} />)

    const move = touch('touchmove', [400])
    act(() => {
      scroller.dispatchEvent(touch('touchstart', [100]))
      scroller.dispatchEvent(move)
    })

    expect(move.defaultPrevented).toBe(true)
  })

  it('leaves a two-finger gesture alone', () => {
    // Two fingers on a page is a zoom, and taking that would be a worse theft
    // than any refresh is worth.
    const onRefresh = vi.fn(async () => {})
    render(<Harness onRefresh={onRefresh} />)

    act(() => {
      scroller.dispatchEvent(touch('touchstart', [100, 200]))
      scroller.dispatchEvent(touch('touchmove', [400, 500]))
      scroller.dispatchEvent(touch('touchend', []))
    })

    expect(onRefresh).not.toHaveBeenCalled()
  })

  it('hands the gesture back when the finger goes up again', () => {
    // Pulled past the start and back: from there the finger is scrolling, and
    // holding on would leave the page unable to move.
    const onRefresh = vi.fn(async () => {})
    render(<Harness onRefresh={onRefresh} />)

    act(() => {
      scroller.dispatchEvent(touch('touchstart', [200]))
      scroller.dispatchEvent(touch('touchmove', [400]))
      scroller.dispatchEvent(touch('touchmove', [150]))
      scroller.dispatchEvent(touch('touchend', []))
    })

    expect(state()).toBe('0:0:idle')
    expect(onRefresh).not.toHaveBeenCalled()
  })

  it('does nothing at all when it is switched off', () => {
    // Desktop: the page has a keyboard and a reload button, and a drag there
    // would be a second way to do something that already has two.
    const onRefresh = vi.fn(async () => {})
    render(<Harness onRefresh={onRefresh} enabled={false} />)

    pull(100, 400)

    expect(onRefresh).not.toHaveBeenCalled()
  })

  it('reports that it is working until the refetch settles', async () => {
    let finish = () => {}
    const onRefresh = vi.fn(() => new Promise<void>((r) => (finish = r)))
    render(<Harness onRefresh={onRefresh} />)

    pull(100, 400)
    expect(state()).toContain('refreshing')

    await act(async () => {
      finish()
    })
    expect(state()).toContain('idle')
  })

  it('does not start a second refresh over the first', async () => {
    // A spinner already turning is an answer to the gesture; pulling again
    // should not queue another round of the same requests.
    let finish = () => {}
    const onRefresh = vi.fn(() => new Promise<void>((r) => (finish = r)))
    render(<Harness onRefresh={onRefresh} />)

    pull(100, 400)
    pull(100, 400)

    expect(onRefresh).toHaveBeenCalledOnce()
    await act(async () => {
      finish()
    })
  })

  it('holds the page open at the threshold while it works', () => {
    // Two things at once. The page must not snap shut over the spinner the
    // instant the finger lifts — and the indicator lives just above the
    // content's top edge, so a page back at zero would tuck it behind the top
    // bar and the refresh would happen with nothing to show for it.
    render(<Harness onRefresh={() => new Promise(() => {})} />)

    pull(100, 900)

    expect(state()).toBe(`0:${REFRESH_THRESHOLD}:refreshing`)
  })

  it('closes the page again once the refetch settles', async () => {
    let finish = () => {}
    const onRefresh = vi.fn(() => new Promise<void>((r) => (finish = r)))
    render(<Harness onRefresh={onRefresh} />)

    pull(100, 900)
    await act(async () => {
      finish()
    })

    expect(state()).toBe('0:0:idle')
  })
})
