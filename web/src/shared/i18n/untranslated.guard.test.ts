import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

import { findStragglers, looksLikeCopy, type Straggler } from './untranslated'

/**
 * No English left where a translation should be.
 *
 * This is the third and last of the guards, and the only one that can see the
 * failure the whole feature was asked to prevent: switching to Vietnamese and
 * meeting English every few screens.
 *
 * The other two cannot. Typed keys catch a *misspelled* key; the dictionary
 * test catches a *missing* translation. A string that was never extracted is
 * neither — it is a literal sitting in a component, and it renders in English
 * with nothing anywhere reporting it. Every occurrence of that class of fault
 * in this app was found by a person reading a screen.
 *
 * oxlint has no rule that could express this, so it is a scan of the source,
 * the same instrument `player-seek.guard.test.ts` uses and for the same reason:
 * a rule that is only written down is forgotten at the next component.
 *
 * ## If this test fails
 *
 * Move the string into `en.ts` and `vi.ts` and call `t()`. Add it to the
 * exemptions below only if it is genuinely not copy — a vendor string, a
 * proper noun, a format placeholder — and say which, in a sentence.
 */

const roots = [join(process.cwd(), 'src'), join(process.cwd(), 'web/src')]
const root = roots.find((path) => existsSync(path))

/**
 * Not copy, and why.
 *
 * Every line is a decision. A guard whose exemption list grows without
 * argument is a guard that has been switched off one file at a time.
 */
const EXEMPT = new Map<string, string>([
  // The dictionaries themselves. Every value in them is English by definition.
  ['shared/i18n/en.ts', 'is the English dictionary'],
  ['shared/i18n/vi.ts', 'quotes English in its notes on how it was translated'],
  ['shared/i18n/untranslated.ts', 'the scanner, whose own examples are English'],
  // A value navigator.vendor is compared against, not a word anybody reads.
  ['features/watch/application/hls-source.ts', 'matches navigator.vendor exactly'],
  // Preset names. The equaliser's terms stay English by decision, and these
  // are the names of curves rather than sentences about them.
  ['features/watch/application/eq-presets.ts', 'audio preset names, kept in English'],
  // Each language is named in its own words on purpose: somebody who switched
  // by accident needs to be able to read their way back.
  ['shared/i18n/index.ts', 'holds each language name in that language'],
])

/**
 * Individual strings that are not copy, wherever they appear.
 *
 * Per string rather than per file, so exempting one line does not quietly
 * exempt everything around it.
 */
const EXEMPT_TEXT = new Map<string, string>([
  // The extension's actual name. CLAUDE.md §6b is emphatic that naming the
  // right one matters here — the wrong one was pulled from the Chrome Web
  // Store as malware — and a translated name would name nothing.
  ['Get cookies.txt', 'the name of a browser extension'],
  // The literal first line of a cookies.txt, shown so somebody can recognise
  // that they have pasted the right thing. Translating it would make the
  // example stop matching the file.
  ['# Netscape HTTP Cookie File…', 'the first line of the file format'],
])

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) {
      if (entry !== 'node_modules') walk(path, out)
    } else if (/\.tsx?$/.test(entry) && !entry.includes('.test.')) {
      out.push(path)
    }
  }
  return out
}

describe('nothing is left untranslated', () => {
  it('scans a real directory', () => {
    // A guard silently scanning nothing passes while proving nothing. vitest
    // can be started from the repo or from web/, so both are tried.
    expect(root).toBeDefined()
    expect(walk(root!).length).toBeGreaterThan(50)
  })

  it('finds no English copy outside the dictionaries', () => {
    const found: Straggler[] = []
    for (const file of walk(root!)) {
      const name = relative(root!, file)
      if (EXEMPT.has(name)) continue
      found.push(
        ...findStragglers(name, readFileSync(file, 'utf8')).filter(
          (s) => !EXEMPT_TEXT.has(s.text),
        ),
      )
    }

    // Reported as file:line and the text, so a failure says where to go rather
    // than only that something is wrong.
    const report = found.map((s) => `${s.file}:${s.line}  [${s.kind}] ${s.text}`)
    expect(report).toEqual([])
  })
})

/**
 * The scanner itself, because a guard nobody has watched find something is a
 * guard nobody knows is wired up.
 */
describe('what counts as copy', () => {
  it('is prose', () => {
    expect(looksLikeCopy('Could not load the model list.')).toBe(true)
    expect(looksLikeCopy('Watch later')).toBe(true)
  })

  it('does not report a Tailwind class list, which has plenty of spaces', () => {
    // Not through `looksLikeCopy`, which only ever sees text nodes and the
    // four attributes — a class list reaches the scan as a *literal*, and
    // there the leading capital is what separates copy from classes.
    expect(findStragglers('x.tsx', 'className="flex h-9 shrink-0 items-center"')).toEqual([])
  })

  it('is not a single word, and that is a known gap', () => {
    // "Save" on a button is copy and this misses it. The alternative flags
    // every id, slug and unit in the codebase, and a guard drowning in false
    // positives is one people switch off. Single words are caught by reading.
    expect(looksLikeCopy('Save')).toBe(false)
  })

  it('finds copy in the four places it reaches a person', () => {
    const source = [
      '<p>Nothing here matches.</p>',
      '<button aria-label="Close player" />',
      '<input placeholder="Search the library" />',
      "const message = 'Could not save that folder.'",
    ].join('\n')

    expect(findStragglers('x.tsx', source).map((s) => s.kind)).toEqual([
      'jsx-text',
      'aria-label',
      'placeholder',
      'literal',
    ])
  })

  it('ignores comments, including the long multi-line ones in this codebase', () => {
    const source = [
      '/**',
      ' * Always a `<Routes>`, never an `<Outlet/>`, and that is the whole',
      ' * reason this reads oddly.',
      ' */',
      '{/* The page underneath, and — when there is no layer over it —',
      '    simply the page. */}',
      '// A rule that is only written down gets forgotten.',
    ].join('\n')

    expect(findStragglers('x.tsx', source)).toEqual([])
  })
})
