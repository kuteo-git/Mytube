import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * Nothing renders a bare `<Link>` — every navigation goes through `PageLink`.
 *
 * React Router carries the screen transition per navigation, as a prop on the
 * link. Spread across thirty call sites that is the shape of mistake this
 * codebase keeps making: it works everywhere somebody remembered and does
 * nothing everywhere they did not, with no error either way and nothing to
 * notice but a screen that appears instead of arriving.
 *
 * Same instrument as `player-seek.guard.test.ts` and for the same reason —
 * oxlint has no rule that could say this, and a rule written only in a comment
 * is forgotten at the next component.
 *
 * ## If this test fails
 *
 * Import `PageLink`/`PageNavLink` from `@/shared/ui/PageLink` instead. Do not
 * add the file below: the exemptions are the two places that legitimately
 * cannot use it.
 */

const roots = [join(process.cwd(), 'src'), join(process.cwd(), 'web/src')]
const root = roots.find((path) => existsSync(path))

const EXEMPT = new Map<string, string>([
  // The wrapper itself, which is where the real Link is used.
  ['shared/ui/PageLink.tsx', 'defines the wrapper'],
])

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) {
      if (entry !== 'node_modules') walk(path, out)
    } else if (/\.tsx$/.test(entry) && !entry.includes('.test.')) {
      out.push(path)
    }
  }
  return out
}

describe('every navigation animates', () => {
  it('scans a real directory', () => {
    expect(root).toBeDefined()
    expect(walk(root!).length).toBeGreaterThan(30)
  })

  it('uses no bare Link or NavLink outside the wrapper', () => {
    const offenders: string[] = []
    for (const file of walk(root!)) {
      const name = relative(root!, file)
      if (EXEMPT.has(name)) continue
      const source = readFileSync(file, 'utf8')
      source.split('\n').forEach((line, index) => {
        // `<PageLink` and `<PageNavLink` both end in `Link`, so the match is
        // anchored on the bracket — and it has to allow end of line, because a
        // multi-line element opens with `<Link` and nothing after it. The
        // first version of this required a following character and so passed
        // on the very case it was written to catch.
        if (/<(Link|NavLink)([\s/>]|$)/.test(line)) {
          offenders.push(`${name}:${index + 1}  ${line.trim()}`)
        }
      })
    }
    expect(offenders).toEqual([])
  })
})
