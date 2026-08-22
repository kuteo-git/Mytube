import { forwardRef, type MouseEvent } from 'react'
import { flushSync } from 'react-dom'
import { Link, NavLink, useNavigate, type LinkProps, type NavLinkProps } from 'react-router-dom'
import type { NavigateFunction, NavigateOptions, To } from 'react-router-dom'

import { canAnimatePages, markDirection } from '@/features/navigation/application/page-transition'

/**
 * A link that animates the screen change.
 *
 * ## Why this calls the API itself
 *
 * React Router accepts `viewTransition` on a `<Link>`, and under this app it
 * does nothing at all: the code that reaches `document.startViewTransition`
 * lives inside `createRouter`, which is the *data* router. This app renders
 * `<BrowserRouter>` with `<Routes>` nested inside the shell, so the prop is
 * accepted by the types and acted on by nobody. It typechecked, it shipped, and
 * the screens changed exactly as they always had.
 *
 * Migrating to a data router to get the prop back would restructure the routing
 * to obtain a wrapper this file can write in ten lines.
 *
 * ## Why flushSync
 *
 * `startViewTransition` takes a callback and snapshots the DOM before and after
 * it. React's update is asynchronous, so a bare `navigate()` inside the
 * callback returns before anything has changed and both snapshots are the old
 * screen. `flushSync` makes the render happen inside the callback, which is the
 * documented way to combine the two.
 *
 * ## One door
 *
 * Thirty links, and a prop on each is the shape of mistake this codebase keeps
 * making — right everywhere somebody remembered, silent everywhere they did
 * not. `page-link.guard.test.ts` fails the build on a bare `<Link>`.
 */

/** Whether this click is the plain one that navigates in-page. */
function isPlainClick(e: MouseEvent<HTMLAnchorElement>, target?: string): boolean {
  return (
    e.button === 0 &&
    !e.metaKey &&
    !e.ctrlKey &&
    !e.shiftKey &&
    !e.altKey &&
    (!target || target === '_self')
  )
}

/**
 * Navigate with the screen sliding, and fall back to navigating.
 *
 * Exported because the back arrow needs the same thing in the other direction,
 * and two implementations of "change the screen" would be two chances for the
 * push and the pop to stop matching.
 */
export function navigateWithTransition(
  navigate: NavigateFunction,
  to: To | number,
  options?: NavigateOptions,
) {
  // `replace` matters and was dropped once already: the back arrow on a
  // cold-opened screen replaces rather than pushes, or going "back" leaves a
  // new entry behind and the next back returns to where you just were.
  const go = () => (typeof to === 'number' ? navigate(to) : navigate(to, options))
  if (!canAnimatePages()) {
    go()
    return
  }
  // Before the transition starts, not after: the direction is read by CSS on
  // the transition's own pseudo-elements, and those exist from the moment
  // startViewTransition is called. Set in an effect afterwards it was always a
  // frame late, which is the whole animation.
  markDirection(typeof to === 'number' && to < 0 ? 'pop' : 'push')
  document.startViewTransition(() => {
    flushSync(go)
  })
}

export const PageLink = forwardRef<HTMLAnchorElement, LinkProps>(function PageLink(
  { children, onClick, to, target, ...rest },
  ref,
) {
  const navigate = useNavigate()
  return (
    <Link
      ref={ref}
      to={to}
      target={target}
      onClick={(e) => {
        onClick?.(e)
        if (e.defaultPrevented || !isPlainClick(e, target)) return
        e.preventDefault()
        navigateWithTransition(navigate, to)
      }}
      {...rest}
    >
      {children}
    </Link>
  )
})

/** The same, for a link that knows whether it is the current page. */
export function PageNavLink({ children, onClick, to, target, ...rest }: NavLinkProps) {
  const navigate = useNavigate()
  return (
    <NavLink
      to={to}
      target={target}
      onClick={(e) => {
        onClick?.(e)
        if (e.defaultPrevented || !isPlainClick(e, target)) return
        e.preventDefault()
        navigateWithTransition(navigate, to)
      }}
      {...rest}
    >
      {children}
    </NavLink>
  )
}

/**
 * A tab, which changes instantly.
 *
 * Tabs are not a hierarchy. A phone slides a screen in when you go *into*
 * something and back out when you leave it, and that reads as depth — but
 * Home, Subscriptions, History and Settings sit beside each other, so sliding
 * between them says one is inside another and none of them is. iOS switches
 * tabs with no motion at all for exactly this reason.
 *
 * It goes through this file rather than importing NavLink directly so the
 * guard still holds: the rule is that navigation lives in one place, not that
 * everything animates.
 */
export function TabLink({ children, ...rest }: NavLinkProps) {
  return <NavLink {...rest}>{children}</NavLink>
}
