import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { BottomNav } from './BottomNav'
import { Children, isValidElement } from 'react'
import { pageRoutes } from '@/app/routes'

/**
 * Every path the router actually serves.
 *
 * Read out of the route table rather than out of the sidebar, which was the
 * first version and was the wrong list: the two carry different subsets on
 * purpose — five fit across a phone, six down a rail, and Subscriptions is a
 * phone entry with no place on the rail. What a link has to be checked against
 * is what the router knows.
 */
const ROUTED = Children.toArray(pageRoutes.props.children)
  .filter(isValidElement)
  .map((route) => (route.props as { path?: string }).path)
  .filter((path): path is string => Boolean(path))

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

  it('carries only the places you move between while browsing', () => {
    // What earns a place here is passing through, not arriving. Saved, Storage
    // and Activity are all somewhere you go on purpose, and all three live at
    // the top of Settings instead — see PhoneOnlyPages there, which is the
    // other half of this rule and the only way any of them can be reached on a
    // phone.
    renderNav()
    const hrefs = screen.getAllByRole('link').map((a) => a.getAttribute('href'))
    expect(hrefs).toEqual(['/', '/subscriptions', '/history', '/settings'])
  })

  it('offers Settings, which has no other way in on a phone', () => {
    renderNav()
    expect(screen.getByRole('link', { name: /settings/i })).toHaveAttribute(
      'href',
      '/settings',
    )
  })

  it('every entry goes somewhere the router actually serves', () => {
    // The charter's rule against dead buttons, checked rather than trusted: a
    // typo in a path renders a link that quietly leads to the not-found page,
    // and nothing about rendering it would say so.
    renderNav()
    const hrefs = screen
      .getAllByRole('link')
      .map((a) => a.getAttribute('href'))
    for (const href of hrefs) {
      expect(ROUTED).toContain(href)
    }
  })

  it('offers Subscriptions, which a phone has no sidebar to list', () => {
    renderNav()
    expect(screen.getByRole('link', { name: /subscriptions/i })).toHaveAttribute(
      'href',
      '/subscriptions',
    )
  })
})
