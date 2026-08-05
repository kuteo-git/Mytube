import { act, fireEvent, render, screen } from '@testing-library/react'
import { useEffect } from 'react'
import { Link, MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useScrollRestoration } from './use-scroll-restoration'

/**
 * The element the app scrolls, faked.
 *
 * The page scrolls inside `<main>` rather than the window, so that a phone's
 * address bar never collapses and drags the top bar with it. jsdom has no
 * layout, so scrollTop and the two heights are backed by plain numbers here —
 * which is all this hook reads or writes.
 */
let scrollY = 0
let contentHeight = 5000
let scroller: HTMLElement

function setHeight(px: number) {
  contentHeight = px
}

beforeEach(() => {
  scrollY = 0
  contentHeight = 5000
  window.sessionStorage.clear()

  scroller = document.createElement('main')
  Object.defineProperty(scroller, 'scrollTop', {
    get: () => scrollY,
    set: (y: number) => {
      scrollY = y
      scroller.dispatchEvent(new Event('scroll'))
    },
    configurable: true,
  })
  Object.defineProperty(scroller, 'scrollHeight', {
    get: () => contentHeight,
    configurable: true,
  })
  Object.defineProperty(scroller, 'clientHeight', { value: 800, configurable: true })
  // HomePage scrolls with an options object; the hook assigns scrollTop. Both
  // have to work, or one of them silently writes nothing.
  scroller.scrollTo = ((a: number | ScrollToOptions, b?: number) => {
    scrollY = typeof a === 'number' ? (b ?? 0) : (a.top ?? 0)
  }) as HTMLElement['scrollTo']
  document.body.appendChild(scroller)
  scrollerForHook = scroller

  // The hook restores across animation frames while it waits for the content to
  // be tall enough. Running them immediately keeps the tests synchronous.
  vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
    cb(0)
    return 1
  })
  vi.stubGlobal('cancelAnimationFrame', () => {})
})

afterEach(() => {
  vi.unstubAllGlobals()
  scroller.remove()
})

function Page({ name }: { name: string }) {
  const navigate = useNavigate()
  return (
    <div>
      <h1>{name}</h1>
      <Link to="/">to home</Link>
      <Link to="/settings">to settings</Link>
      <Link to="/watch/abc">to video</Link>
      <button type="button" onClick={() => navigate(-1)}>
        back
      </button>
    </div>
  )
}

/** Null while a layer is over the page, as the shell does. */
let scrollerForHook: HTMLElement | null = null

function App() {
  useScrollRestoration(scrollerForHook)
  return (
    <Routes>
      <Route path="/" element={<Page name="home" />} />
      <Route path="/settings" element={<Page name="settings" />} />
      <Route path="/watch/:id" element={<Page name="video" />} />
    </Routes>
  )
}

function renderApp() {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <App />
    </MemoryRouter>,
  )
}

/** Click, and let React flush the navigation it causes. */
function click(name: string) {
  act(() => {
    fireEvent.click(
      screen.getByRole(name === 'back' ? 'button' : 'link', { name }),
    )
  })
}

/** Move the scroller and let the hook's listener record it. */
function scrollTo(y: number) {
  act(() => {
    scrollY = y
    scroller.dispatchEvent(new Event('scroll'))
  })
}

describe('scrolling across a navigation', () => {
  it('opens a page you drill into at the top, however far down you were', () => {
    renderApp()
    scrollTo(900)

    click('to video')

    expect(screen.getByRole('heading', { name: 'video' })).toBeInTheDocument()
    expect(scrollY).toBe(0)
  })

  it('gives every tab its own position', () => {
    // The movement people make constantly, and the one a plain "scroll to top
    // on navigate" gets wrong: leave Home halfway down, look at Settings, come
    // back — and be where you were.
    renderApp()
    scrollTo(900)

    click('to settings')
    expect(scrollY).toBe(0)
    scrollTo(300)

    click('to home')
    expect(screen.getByRole('heading', { name: 'home' })).toBeInTheDocument()
    expect(scrollY).toBe(900)

    click('to settings')
    expect(scrollY).toBe(300)
  })

  it('takes you to the top when you tap the tab you are already on', () => {
    renderApp()
    scrollTo(900)

    click('to home')

    expect(scrollY).toBe(0)
  })

  it('puts you back where you were when you go back', () => {
    renderApp()
    scrollTo(900)

    click('to video')
    expect(scrollY).toBe(0)

    click('back')

    expect(screen.getByRole('heading', { name: 'home' })).toBeInTheDocument()
    expect(scrollY).toBe(900)
  })

  it('waits for the page to be tall enough before landing', () => {
    renderApp()
    scrollTo(3000)
    click('to video')

    // Back to a feed whose rows have not been rebuilt yet. Landing now would
    // put the viewer at the bottom of a half-built page, which is
    // indistinguishable from the position having been forgotten.
    setHeight(1000)
    click('back')

    expect(scrollY).toBe(0)
  })

  it('records a position per history entry, not per path', () => {
    // Two visits to the same page are two entries with two positions. Keyed by
    // path they would overwrite each other, and going back twice would land on
    // the same offset both times.
    renderApp()
    scrollTo(400)
    click('to video')
    scrollTo(1500)

    click('back')
    expect(scrollY).toBe(400)

    // Enumerated through the Storage API rather than Object.keys, which does
    // not see them.
    const keys: string[] = []
    for (let i = 0; i < window.sessionStorage.length; i++) {
      const k = window.sessionStorage.key(i)
      if (k?.startsWith('yt-scroll:')) keys.push(k)
    }
    expect(keys.length).toBeGreaterThanOrEqual(2)
  })

  it('wins against a page that scrolls itself on mount', () => {
    // The fault this caught for real. HomePage resets to the top when the topic
    // chip changes, and that effect fired on mount too — which is exactly what
    // happens when you come back to Home. Returning was restored and then
    // thrown to the top a moment later, on the one page people scroll most.
    //
    // The page is fixed, but the ordering it relies on is worth pinning: a
    // child's effects run before its parent's, so the restore here is the last
    // word. If that ever stops being true, a scroll-on-mount anywhere in the
    // app would silently defeat this hook.
    function Selfish() {
      useEffect(() => {
        // The exact call HomePage makes, options object and all.
        scroller.scrollTo({ top: 0 })
      }, [])
      return (
        <div>
          <h1>selfish</h1>
          <Link to="/settings">to settings</Link>
          <Link to="/">to home</Link>
        </div>
      )
    }
    function SelfishApp() {
      useScrollRestoration(scrollerForHook)
      return (
        <Routes>
          <Route path="/" element={<Selfish />} />
          <Route path="/settings" element={<Page name="settings" />} />
        </Routes>
      )
    }
    render(
      <MemoryRouter initialEntries={['/']}>
        <SelfishApp />
      </MemoryRouter>,
    )
    scrollTo(900)
    click('to settings')
    click('to home')

    expect(scrollY).toBe(900)
  })

  it('leaves the page alone on the way back out of a layer', () => {
    // A layer over the page — the phone's watch screen — hands this hook no
    // scroller, because `<main>` goes on holding the page underneath and must
    // not be touched. Coming back out, there is still nothing to restore: the
    // scroller never moved. Restoring anyway resolved a POP with no recorded
    // position to zero and threw the page to the top, which is exactly the
    // position it had been holding correctly all along.
    const { rerender } = render(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>,
    )
    scrollTo(900)

    // The layer opens: no scroller to work on.
    scrollerForHook = null
    rerender(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>,
    )
    expect(scrollY).toBe(900)

    // And closes again.
    scrollerForHook = scroller
    rerender(
      <MemoryRouter initialEntries={['/']}>
        <App />
      </MemoryRouter>,
    )

    expect(scrollY).toBe(900)
  })

  it('takes the browser out of the argument', () => {
    // Left on automatic the browser also restores on a back, a frame or two
    // after this does and from its own idea of the position — so the page
    // lands, then jumps.
    renderApp()
    expect(window.history.scrollRestoration).toBe('manual')
  })
})
