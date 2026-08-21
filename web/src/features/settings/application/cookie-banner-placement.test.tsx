import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AppShell } from '@/app/AppShell'

/**
 * Where the expired-session banner sits, and why that is a layout question.
 *
 * The banner was rendered in the shell's flow, above `<main>`, on the reasoning
 * that it belongs "above everything". The top bar is `absolute inset-x-0 top-0`
 * and therefore does not move for anything — so the banner took 44px of layout
 * space at the very top and was then painted over by the bar. Measured in the
 * browser:
 *
 *   DIV.flex items-center...   top=0   h=44   position=static   <- the banner
 *   HEADER.chrome-blur         top=0   h=56   position=absolute
 *   MAIN                       top=44
 *
 * Two faults from one placement. Every page began 44px lower than it should,
 * which is what a viewer sees as a gap under the search bar — on the phone and
 * on the desktop alike, since this has nothing to do with the safe area. And
 * the warning itself was invisible: this household's session expired on
 * 2026-08-16 and nothing said so for five days, which is the exact outcome the
 * component was written to prevent.
 *
 * So it moves inside the scroller, which is the one place that already reserves
 * room for the bar. CLAUDE.md's note on the chip row says it plainly: the top
 * bar's height belongs in exactly one place, and that place is the scroller.
 */

let state: string

vi.mock('@/features/settings/infrastructure/accountRepository', () => ({
  accountRepository: {
    get: vi.fn(async () => ({ userId: 'u_luc', label: '', state, lastResult: '', lastScanAt: '' })),
  },
}))

vi.mock('@/features/catalog/application/queries', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return { ...actual }
})

beforeEach(() => {
  state = 'EXPIRED'
  window.localStorage.clear()
})

function renderShell() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route element={<AppShell />}>
            <Route path="/" element={<div>a page</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('the expired-session banner', () => {
  it('is inside the scroller, not above it', async () => {
    renderShell()

    const banner = await screen.findByRole('status')
    const main = document.querySelector('main')
    expect(main).toBeTruthy()

    // The whole bug in one assertion. Outside the scroller it displaces every
    // page by its own height and is painted over by the bar; inside, it sits in
    // the room already reserved and can be read.
    expect(main!.contains(banner)).toBe(true)
  })

  it('leaves nothing in the shell that could displace the scroller', async () => {
    renderShell()
    await screen.findByRole('status')

    const main = document.querySelector('main')!
    const shell = main.parentElement!

    // Anything in the shell's flow before the scroller pushes it down, and the
    // bar cannot move out of the way because it is absolute. So there must be
    // nothing in flow there at all.
    //
    // Read from the class list rather than from `getComputedStyle`: jsdom loads
    // no stylesheet, so every Tailwind `absolute` computes as `static` and the
    // honest-looking version of this check would call the top bar a fault too.
    const inFlowBefore = Array.from(shell.children)
      .slice(0, Array.from(shell.children).indexOf(main))
      .filter((el) => !/\b(absolute|fixed)\b/.test(String(el.className)))
      .map((el) => el.tagName + '.' + String(el.className).slice(0, 40))

    expect(inFlowBefore).toEqual([])
  })

  it('says nothing at all while the session is working', async () => {
    state = 'OK'
    renderShell()

    await waitFor(() => expect(document.querySelector('main')).toBeTruthy())
    // A banner that is up while things work is a banner nobody reads on the day
    // they need to.
    expect(screen.queryByRole('status')).toBeNull()
  })
})
