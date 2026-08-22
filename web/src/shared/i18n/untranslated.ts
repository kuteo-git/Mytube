/**
 * Finding English that never became a translation key.
 *
 * This is the one failure the other two i18n checks cannot see. A key that is
 * misspelled does not compile; a key missing from the Vietnamese dictionary
 * fails the dictionary test. But a string that was *never extracted* is not a
 * key at all — it is a literal sitting in JSX, and it renders in English on a
 * screen the viewer asked to be in Vietnamese. Nothing anywhere reports it.
 *
 * So the check is a scan of the source, the same instrument and for the same
 * reason as `player-seek.guard.test.ts`: oxlint has no rule that could express
 * this, and a rule that is only written down is forgotten at the next
 * component.
 *
 * ## Narrow on purpose
 *
 * It looks at four places where copy actually reaches a person — JSX text,
 * `aria-label`, `placeholder`, `title` — and only at strings with a space in
 * them. A scan that flags `className="flex gap-2"` and `data-testid="state"` is
 * a scan everyone silences, and a guard full of exemptions guards nothing.
 *
 * Kept apart from the test that runs it so the rule itself can be tested: a
 * scanner nobody has watched find something is a scanner nobody knows works.
 */

/** One piece of English that should have been a key. */
export interface Straggler {
  file: string
  line: number
  text: string
  /** Which of the four places it was found in, for the failure message. */
  kind: 'jsx-text' | 'aria-label' | 'placeholder' | 'title' | 'literal'
}

/**
 * At least two letters and at least one space between words.
 *
 * The space is what separates prose from the everything else that lives in
 * these positions: css classes are caught by their own attribute names, but
 * ids, slugs, units and single words like "px" are not, and every one of them
 * would be a false positive. Real copy that is a single word — a button
 * reading "Save" — is missed by this and has to be caught by review; that is
 * the deliberate trade, because the alternative is a guard nobody trusts.
 */
const PROSE = /[A-Za-z]{2,}\s+[A-Za-z]/

/**
 * A quoted string that is copy rather than anything else in this codebase.
 *
 * The discriminator is the leading capital, and it is doing more work than it
 * looks. Most strings in a React file are Tailwind class lists, which have
 * plenty of spaces — `'flex h-9 shrink-0 items-center'` — and are entirely
 * lowercase. Copy begins with a capital because it is a sentence or a label.
 *
 * Measured over the whole app when this was written: 170 matches, every one of
 * them real copy and not one false positive. Widening it further was tried and
 * is what turns a guard into a list of exemptions.
 */
const LITERAL = /'([A-Z][^']*\s[^']*)'|"([A-Z][^"]*\s[^"]*)"/g

/**
 * Text that is English prose rather than markup, punctuation or an expression.
 *
 * `{t('…')}` and every other JSX expression is excluded by construction: this
 * only ever sees the text *between* tags, and an expression is not text.
 */
export function looksLikeCopy(text: string): boolean {
  const trimmed = text.trim()
  if (!PROSE.test(trimmed)) return false
  // A line of prose that is entirely inside an expression is markup, not copy.
  if (trimmed.startsWith('{') && trimmed.endsWith('}')) return false
  return true
}

/**
 * Scan one file's source for English that should have been extracted.
 *
 * Line-based rather than parsed. A JSX parser would be more precise and would
 * also be a second, silently diverging model of the code; this reports a file
 * and a line, which is all anybody needs to go and fix it.
 */
export function findStragglers(file: string, source: string): Straggler[] {
  const out: Straggler[] = []
  const lines = source.split('\n')

  // Whether the scan is inside a block comment.
  //
  // Tracked as state rather than judged line by line, because this codebase's
  // comments are long and their continuation lines are ordinary prose — a JSX
  // comment's middle lines start with neither `//` nor `*`, and one of them
  // reading "Always a `<Routes>`, never an `<Outlet/>`" was reported as
  // untranslated copy on the first run.
  let inBlock = false

  lines.forEach((line, index) => {
    const at = index + 1
    const code = line.trim()

    // What has already been reported on this line.
    //
    // An `aria-label="Close player"` matches the attribute rule and then the
    // literal rule as well — it is, after all, a quoted string starting with a
    // capital. Reported twice it doubles the length of every failure and makes
    // the count meaningless, and the attribute name is the more useful of the
    // two answers.
    const seen = new Set<string>()

    const wasInBlock = inBlock
    if (inBlock) {
      if (line.includes('*/')) inBlock = false
      return
    }
    if ((code.includes('/*') || code.includes('{/*')) && !line.includes('*/')) {
      inBlock = true
      return
    }

    // Comments are not copy. Checked before anything else, because this file's
    // own prose would otherwise flag every guard in the codebase.
    if (wasInBlock || code.startsWith('//') || code.startsWith('*') || code.startsWith('/*')) return
    if (code.startsWith('{/*') && code.endsWith('*/}')) return

    for (const [kind, pattern] of [
      ['aria-label', /aria-label="([^"]+)"/g],
      ['placeholder', /placeholder="([^"]+)"/g],
      ['title', /\stitle="([^"]+)"/g],
    ] as const) {
      for (const match of line.matchAll(pattern)) {
        if (looksLikeCopy(match[1]) && !seen.has(match[1])) {
          seen.add(match[1])
          out.push({ file, line: at, text: match[1], kind })
        }
      }
    }

    // JSX text: what sits between a closing bracket and an opening one, on the
    // same line. Text spanning several lines is caught by whichever of its
    // lines happens to hold two words, which is enough to point at it.
    for (const match of line.matchAll(/>([^<>{}]+)</g)) {
      const text = match[1].trim()
      if (looksLikeCopy(match[1]) && !seen.has(text)) {
        seen.add(text)
        out.push({ file, line: at, text, kind: 'jsx-text' })
      }
    }

    // And copy that never reaches JSX as text: a label passed as a prop, a
    // message in a ternary, a toast. This is where most of it actually lives —
    // 170 of the app's 243 strings, against 73 in text nodes — so a scan
    // without it would have passed while two thirds of the app stayed English.
    for (const match of line.matchAll(LITERAL)) {
      const text = (match[1] ?? match[2]).trim()
      if (PROSE.test(text) && !seen.has(text)) {
        seen.add(text)
        out.push({ file, line: at, text, kind: 'literal' })
      }
    }
  })

  return out
}
