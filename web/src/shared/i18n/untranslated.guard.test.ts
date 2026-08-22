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
  // Apple's AVAudioUnitReverb preset names, verbatim. CLAUDE.md §5 records
  // that these are Apple's enum and not descriptions.
  ['features/watch/application/reverb-presets.ts', 'Apple reverb preset names'],
  // The name of a text-to-speech voice, and the label of a subtitle track.
  // Both are content: what the track calls itself, read as it is written.
  ['features/watch/application/narration.ts', 'a TTS voice name'],
  ['features/settings/application/settings-prefs.ts', 'a TTS voice name'],
  ['features/watch/domain/subtitle-language.ts', 'a subtitle track label'],
  // "Tr" is the Vietnamese abbreviation for triệu. It is in the file because
  // the file is what holds both languages' abbreviations.
  ['shared/lib/format.ts', 'holds both languages abbreviations'],
  // Diagnostics — "server returned 500", "got 0/40 lines, all empty" — which
  // are interpolated into an already-translated sentence the way an HTTP
  // status is. The sentence around them is Vietnamese; the detail is what it
  // is, and inventing a Vietnamese rendering of an upstream fault would make
  // it harder to search for, not easier to read.
  ['features/watch/application/narration-batch.ts', 'diagnostic detail, shown inside translated copy'],
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
  // Chip identities, compared against the active chip, so they must be the
  // same string whatever language is on screen. What the viewer reads is
  // looked up where the chip is drawn.
  ['All', 'a chip identity, not its label'],
  ['Live', 'a chip identity, not its label'],
  // Placeholders showing the shape of a value. Translating them would make
  // the example stop being an example.
  ['/Volumes/Data2/Youtube', 'an example path'],
  ['http://host:port', 'an example URL'],
  // The equaliser's own name, kept in English with the rest of its vocabulary.
  ['Equalizer', 'kept in English by decision'],
  // Appended to a subtitle track's own name, which is content.
  ['(auto)', 'marks a machine-generated track'],
  // The seeded profile name in a default, not copy.
  ['Luc', 'a seeded profile name'],
])

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) {
      if (entry !== 'node_modules') walk(path, out)
    } else if (
      /\.tsx?$/.test(entry) &&
      !entry.includes('.test.') &&
      // Generated protobuf. Nobody writes it and nobody reads it; one of its
      // files is a single base64 line thousands of characters long.
      !path.includes('/api/gen/')
    ) {
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

  /**
   * A single word is copy, and pretending otherwise is what let the app ship
   * half translated.
   *
   * This test used to assert the opposite, with a comment calling it a known
   * gap that review would cover. It did not: most of an interface *is* single
   * words — Home, Settings, Subscribe, Share, Search, Back — so the guard
   * passed while the tab bar, the chips and the watch page were all still in
   * English, and the person who found out was the one using the app.
   */
  it('is a single word, because most of an interface is', () => {
    expect(looksLikeCopy('Save')).toBe(true)
    expect(findStragglers('x.tsx', "{ label: 'Subscriptions', to: '/subs' }")).toHaveLength(1)
  })

  it('is not a constant, an identifier, a header or something compared against', () => {
    const source = [
      "const method = 'POST'",
      "if (state === 'RUNNING') return",
      "headers: { 'Content-Type': 'application/json' }",
      "if (e.key === 'Escape') close()",
      "throw new Error('TooFastError')",
    ].join('\n')

    expect(findStragglers('x.ts', source)).toEqual([])
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
