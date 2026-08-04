import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { BottomNav } from './BottomNav'
import { SIDEBAR_ROUTES } from './Sidebar'

function renderNav() {
  return render(
    <MemoryRouter>
      <BottomNav />
    </MemoryRouter>,
  )
}

describe('the mobile bottom bar', () => {
  it('holds no more than five entries', () => {
    // The ceiling is a finger, not a preference: five across a phone leaves each
    // target around 44px wide, and a sixth takes every one of them below it.
    expect(screen.queryAllByRole('link')).toHaveLength(0)
    renderNav()
    expect(screen.getAllByRole('link').length).toBeLessThanOrEqual(5)
  })

  it('offers Settings, which has no other way in on a phone', () => {
    renderNav()
    expect(screen.getByRole('link', { name: /settings/i })).toHaveAttribute(
      'href',
      '/settings',
    )
  })

  it('every entry goes somewhere the sidebar also knows about', () => {
    // The charter's rule against dead buttons, checked rather than trusted: a
    // typo in a path renders a link that quietly leads to the not-found page.
    renderNav()
    const hrefs = screen
      .getAllByRole('link')
      .map((a) => a.getAttribute('href'))
    for (const href of hrefs) {
      expect(SIDEBAR_ROUTES).toContain(href)
    }
  })
})
