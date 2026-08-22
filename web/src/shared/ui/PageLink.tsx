import { forwardRef } from 'react'
import { Link, NavLink, type LinkProps, type NavLinkProps } from 'react-router-dom'

import { canAnimatePages } from '@/features/navigation/application/page-transition'

/**
 * A link that animates the screen change.
 *
 * One door rather than a prop on thirty links. React Router carries the
 * transition per navigation — `<Link viewTransition>` — and spreading that
 * across every call site is the shape of mistake this codebase has made
 * repeatedly: it works everywhere somebody remembered and does nothing
 * everywhere they did not, with no error either way. `page-link.guard.test.ts`
 * fails the build on a bare `react-router-dom` Link inside a feature.
 *
 * `viewTransition` is asked for only where the browser has the API. React
 * Router falls back cleanly on its own, but asking for something a browser
 * cannot do is worth not doing rather than worth relying on.
 */
export const PageLink = forwardRef<HTMLAnchorElement, LinkProps>(function PageLink(
  { children, ...rest },
  ref,
) {
  return (
    <Link ref={ref} viewTransition={canAnimatePages()} {...rest}>
      {children}
    </Link>
  )
})

/** The same, for a link that knows whether it is the current page. */
export function PageNavLink({ children, ...rest }: NavLinkProps) {
  return (
    <NavLink viewTransition={canAnimatePages()} {...rest}>
      {children}
    </NavLink>
  )
}
