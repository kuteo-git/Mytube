import { readFileSync } from 'node:fs'
import path from 'node:path'
import { transform } from 'lightningcss'
import { describe, expect, it } from 'vitest'

// The blur has to survive the minifier, not merely be written down.
//
// lightningcss deduplicates a property against its own `-webkit-` alias by
// keeping whichever was written **last** and deleting the other. So the
// conventional order — standard first, prefix after as a fallback — ships the
// prefixed form alone. That was invisible for as long as Chrome honoured
// `-webkit-backdrop-filter`; Chrome 153 removed the alias, and every pane in
// the app stopped blurring on the same day, with the CSS still saying blur(32px)
// and `getComputedStyle` answering `none`.
//
// Tailwind's own `backdrop-filter` utility is the control: it writes the prefix
// first and survives, which is the order this file enforces.
//
// Asserted against the *output* rather than the source, because the source was
// never wrong — the source says blur in both builds.
const css = readFileSync(path.resolve(__dirname, 'index.css'), 'utf8')

const minified = () =>
  transform({
    filename: 'index.css',
    code: Buffer.from(css),
    minify: true,
  }).code.toString()

describe('backdrop blur survives the build', () => {
  const classes = ['chrome-blur', 'panel-blur']

  it.each(classes)('.%s still carries an unprefixed backdrop-filter', (name) => {
    const out = minified()
    const rule = new RegExp(`\\.${name}\\{([^}]*)\\}`, 'g')
    const bodies = [...out.matchAll(rule)].map((m) => m[1])
    expect(bodies.length, `no .${name} rule in the built CSS`).toBeGreaterThan(0)

    const blurred = bodies.some((b) => /(?:^|;)backdrop-filter:/.test(b))
    expect(
      blurred,
      `.${name} ships only the dead -webkit- alias: ${bodies.join(' | ')}`,
    ).toBe(true)
  })

  // The source rule has to ask for it in the first place, or the check above
  // would pass on a class that simply stopped blurring.
  it.each(classes)('.%s asks for a blur in the source', (name) => {
    const rule = new RegExp(`\\.${name}\\s*\\{[\\s\\S]*?\\}`)
    const body = css.match(rule)?.[0] ?? ''
    expect(body).toMatch(/backdrop-filter:\s*blur\(/)
  })
})
