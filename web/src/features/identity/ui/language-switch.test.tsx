import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AccountMenu } from './AccountMenu'
import i18n from '@/shared/i18n'

/**
 * Changing language from the avatar, and the two things about it that are
 * decisions rather than details.
 */

vi.mock('../application/use-profile', () => ({
  useProfiles: () => ({ data: [{ id: 'u_luc', name: 'Luc' }] }),
  useCurrentProfile: () => ({ id: 'u_luc', choose: vi.fn() }),
}))

function renderMenu() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <AccountMenu />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  window.localStorage.clear()
})

afterEach(async () => {
  // The whole suite runs in English (see test/setup.ts). A test that switches
  // language and leaves it switched would translate every assertion that runs
  // after it, in a different file, with nothing to say why.
  await act(async () => {
    await i18n.changeLanguage('en')
  })
})

describe('the language switch', () => {
  /**
   * Each language is named in its own words, and that is not decoration.
   *
   * Somebody who pressed the wrong row is now looking at an interface they
   * cannot read. "English", written in English, is the way back out —
   * translating these two labels would close the only door.
   */
  it('names each language in itself, in either language', async () => {
    renderMenu()
    fireEvent.click(screen.getByRole('button', { name: /account/i }))

    expect(screen.getByText('English')).toBeInTheDocument()
    expect(screen.getByText('Tiếng Việt')).toBeInTheDocument()

    await act(async () => {
      await i18n.changeLanguage('vi')
    })

    expect(screen.getByText('English')).toBeInTheDocument()
    expect(screen.getByText('Tiếng Việt')).toBeInTheDocument()
  })

  it('changes the interface and remembers the choice', async () => {
    renderMenu()
    fireEvent.click(screen.getByRole('button', { name: /account/i }))

    await act(async () => {
      fireEvent.click(screen.getByText('Tiếng Việt'))
    })

    await waitFor(() => expect(i18n.language).toBe('vi'))
    // Remembered per device, so the next visit opens in Vietnamese without
    // waiting on a request — which is also why there is no flash of English.
    expect(window.localStorage.getItem('yt-language-v1')).toBe('vi')
  })

  /**
   * `documentElement.lang` is what a screen reader picks a voice from. Left at
   * "en", Vietnamese is read aloud by an English voice, which is not merely
   * wrong but unintelligible.
   */
  it('tells the document what language it is in', async () => {
    renderMenu()
    fireEvent.click(screen.getByRole('button', { name: /account/i }))

    await act(async () => {
      fireEvent.click(screen.getByText('Tiếng Việt'))
    })

    expect(document.documentElement.lang).toBe('vi')
  })
})
