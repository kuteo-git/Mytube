import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { PlayerProvider } from '@/features/watch/application/player-context'
import { MOBILE_BREAKPOINT } from '@/features/watch/application/player-geometry'
import { SettingsPage } from './SettingsPage'

vi.mock('@/features/settings/application/queries', () => ({
  useVoices: () => ({ data: ['Ngọc Linh'] }),
  useTranslateConfig: () => ({ data: undefined }),
  useSaveTranslateConfig: () => ({ mutate: () => {}, isPending: false }),
  useTestTranslate: () => ({ mutate: () => {}, isPending: false, data: undefined }),
  useTranslateModels: () => ({ mutate: () => {}, data: [], isPending: false, isPaused: false }),
  useRanking: () => ({ data: {}, isPending: false, isError: false, refetch: () => {} }),
  useSaveRanking: () => ({ mutate: () => {}, isPending: false }),
}))

vi.mock('@/features/settings/ui/FeedMixSettings', () => ({
  FeedMixSettings: () => <div>feed mix panel</div>,
}))

vi.mock('@/features/settings/ui/AdvancedSettings', () => ({
  AdvancedSettings: () => <div>advanced panel</div>,
}))

function renderAt(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true })
  Object.defineProperty(window, 'innerHeight', { value: 800, configurable: true })
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <PlayerProvider isWatch={false}>
          <SettingsPage />
        </PlayerProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

const phone = () => renderAt(MOBILE_BREAKPOINT - 40)
const desktop = () => renderAt(1440)

beforeEach(() => {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  }))
})

describe('Settings on a phone', () => {
  it('is a list of screens rather than a stack of panels', () => {
    // Three panels of sliders down a 390px column is a page you scroll through
    // hunting for the one control you came for, and it is never the one on top.
    phone()
    expect(screen.queryByText('feed mix panel')).not.toBeInTheDocument()
    expect(screen.queryByText('advanced panel')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Home feed' })).toHaveAttribute(
      'href',
      '/settings/feed',
    )
  })

  it('leads to everything a phone cannot reach any other way', () => {
    // The other half of the bottom bar's ceiling. Storage's banner appears only
    // above 75% full and can be dismissed, Activity's bell lives on the desktop
    // header, and Saved has nothing else at all — so dropping one from the bar
    // without landing it here would strand it.
    phone()
    const hrefs = screen.getAllByRole('link').map((a) => a.getAttribute('href'))
    expect(hrefs).toEqual([
      '/saved',
      '/storage',
      '/activity',
      // Everything belonging to this account, in one group and in the order
      // the desktop rail uses. The profile leads it: it decides whose every
      // other setting here is. A phone has no sidebar and the bottom bar is
      // full at five, so this is the way in — the avatar being the other door
      // to the same room.
      '/profile',
      '/account',
      '/watch-later',
      '/playlists',
      '/settings/feed',
      '/settings/narration',
      '/settings/translation',
      '/settings/advanced',
    ])
  })

  it('leads with the one entry that is content', () => {
    // Saved is somewhere you go to watch something you kept. The rest are read
    // when you have decided to change or check something.
    phone()
    expect(screen.getAllByRole('link')[0]).toHaveAccessibleName('Saved')
  })
})

describe('Settings on a desktop', () => {
  it('shows the panels themselves, with room for them', () => {
    desktop()
    expect(screen.getByText('feed mix panel')).toBeInTheDocument()
    expect(screen.getByText('advanced panel')).toBeInTheDocument()
  })

  it('adds no links, because the sidebar already has them', () => {
    // A second route to the same page would just be clutter next to a rail that
    // lists all of them.
    desktop()
    const hrefs = screen.queryAllByRole('link').map((a) => a.getAttribute('href'))
    expect(hrefs).not.toContain('/saved')
    expect(hrefs).not.toContain('/settings/feed')
  })
})
