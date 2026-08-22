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
const PROSE = /[A-Za-z]{2,}/

/**
 * A quoted string that might be copy.
 *
 * The leading capital does most of the work: Tailwind class lists have plenty
 * of spaces — `'flex h-9 shrink-0 items-center'` — and are entirely lowercase.
 *
 * **This once also demanded a space, and that was the bug.** It read as a
 * reasonable trade at the time and was written down as one: single words would
 * be caught by review. They were not. Most of a user interface *is* single
 * words — Home, Settings, Subscribe, Share, Search, Saved, Back — so the guard
 * passed on an app that was half English, and the person who noticed was the
 * one using it. That is precisely the failure it exists to prevent, and it is
 * why the exclusions below are enumerated instead: it is better to name what
 * is not copy than to guess at what is.
 */
const LITERAL = /'([A-Z][A-Za-z][^']*)'|"([A-Z][A-Za-z][^"]*)"/g

/** SCREAMING_CASE and header names: a value, never something anybody reads. */
const SHOUTED = /^[A-Z0-9_ -]+$/

/** `TooFastError`, `MediaSource` — an identifier written as a string. */
const IDENTIFIER = /^[A-Za-z]+([A-Z][a-z]+)+$/

/** `Content-Type`, `Cache-Control`: hyphenated with no space. */
const HEADER = /^[A-Za-z]+(-[A-Za-z]+)+$/

/**
 * A literal being compared or switched on is a value, not copy.
 *
 * `event.key === 'Escape'` and `case 'Saved'` are the shapes; translating
 * either would break the code rather than the wording.
 */
const COMPARED = /(===|!==|==|!=|case|includes\(|startsWith\(|endsWith\()$/

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
  // The tail of a JSX expression that happens to span a `>`: `{count > 0 &&`
  // leaves "0 && available" between a bracket and the next tag. Operators do
  // not appear in copy, and copy that contains one is not worth the exemption.
  if (/(&&|\|\||\?\?|=>|===|!==)/.test(trimmed)) return false
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
    // A module path is not copy, and every file starts with a screenful.
    if (code.startsWith('import ') || / from '/.test(code)) return
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
    //
    // Only in .tsx. In a .ts file the same shape is a generic — `Promise<T>`,
    // `(path: string): Promise<Response>` — and reading those as copy is how a
    // guard starts reporting the language it is written in.
    for (const match of file.endsWith('.tsx') ? line.matchAll(/>([^<>{}]+)</g) : []) {
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
      if (seen.has(text)) continue
      if (SHOUTED.test(text) || IDENTIFIER.test(text) || HEADER.test(text)) continue
      if (COMPARED.test(line.slice(0, match.index).trimEnd())) continue
      seen.add(text)
      out.push({ file, line: at, text, kind: 'literal' })
    }
  })

  return out
}
