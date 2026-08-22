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
  kind: 'jsx-text' | 'aria-label' | 'placeholder' | 'title' | 'literal' | 'template'
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
 * A template literal with two real words in it.
 *
 * The scan read `'` and `"` and not backticks, which is where copy goes the
 * moment it has to interpolate anything — and copy that interpolates is the
 * copy most worth checking, because it is a sentence rather than a label.
 * Six of them were live on screen while every other check was green:
 * "View more (3)", "Playing from …", "Writable. 235 GB free, 914 videos
 * already there."
 *
 * Two words rather than one, unlike the quoted rule: a backtick string is far
 * more often a URL, a class list or a key path, and those have no two adjacent
 * words. `${...}` counts as a break, so "Playing from ${label}" is caught on
 * "Playing from" and `${a}/${b}` is not caught at all.
 */
const TEMPLATE = /`([^`]*[A-Za-z]{2,}\s+[A-Za-z]{2,}[^`]*)`/g

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
  // Code, not words.
  //
  // Scanning JSX across whole lines means every `=>`, every `a > b` and every
  // generic produces a span that ends at the next `<`. Those spans are full of
  // punctuation that copy never has — `= useStorage()`, `) : (`, `void`,
  // `0 ? fmt.views(v) : null` — and the cheapest reliable way to tell them
  // apart is to insist that copy is made of words.
  if (/[=;{}[\]`|&<>]|=>|\+\+|\.\w/.test(trimmed)) return false
  // A lone keyword or fragment of a statement.
  if (/^(return|void|null|undefined|true|false|export|function|const|let|async|await)\b/.test(trimmed))
    return false
  // Must actually contain a word, not just punctuation and digits.
  if (!/[A-Za-z]{2,}/.test(trimmed)) return false
  // Copy does not open with a closing bracket or a colon. Those are the tail
  // of an expression the scan cut through — `) : null`, `: PillProps)`.
  if (/^[)(:;,]/.test(trimmed)) return false
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

  if (file.endsWith('.tsx')) out.push(...findJSXText(file, source))

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


    // And copy that never reaches JSX as text: a label passed as a prop, a
    // message in a ternary, a toast. This is where most of it actually lives —
    // 170 of the app's 243 strings, against 73 in text nodes — so a scan
    // without it would have passed while two thirds of the app stayed English.
    for (const match of line.matchAll(TEMPLATE)) {
      const text = match[1].trim()
      if (seen.has(text)) continue
      // A path or a query string is not copy however many words it has.
      if (/^[/?]|^https?:/.test(text)) continue
      // A *type*-level template literal — `nav.${keyof Dictionary['nav']}` —
      // is a type, not a string, and nobody reads it.
      if (text.includes('keyof ') || code.startsWith('type ')) continue
      seen.add(text)
      out.push({ file, line: at, text, kind: 'template' })
    }

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


/**
 * Copy sitting between tags, however it is laid out.
 *
 * Read across the whole file rather than line by line, because line by line
 * missed the two shapes copy most often takes:
 *
 *   <p>                              <Pill>
 *     When storage fills past …        From {channel.name}
 *     are removed from disk.         </Pill>
 *   </p>
 *
 * A paragraph's middle lines carry no bracket at all, and text beside an
 * expression is separated from the nearest bracket by a `}`. Both were live on
 * screen while the scan reported nothing — the storage explanation, the
 * activity empty state, "All", "From …", "{n} Comments".
 *
 * `{…}` is treated as a boundary rather than as content, so an expression's
 * code is never mistaken for words and the text on either side of it is still
 * seen.
 *
 * `.tsx` only: in a `.ts` file the same angle brackets are a generic.
 */
function findJSXText(file: string, source: string): Straggler[] {
  const out: Straggler[] = []
  // Comments first, and replaced by newlines rather than removed, so the line
  // numbers a failure reports still point at the right place.
  const code = source
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/\/\/[^\n]*/g, (m) => ' '.repeat(m.length))

  for (const match of code.matchAll(/>([^<>]*)</g)) {
    const span = match[1]
    if (!span.trim()) continue
    // Where this span starts, for the report.
    const at = code.slice(0, match.index).split('\n').length

    // Braces bound an expression: split on them and keep what is outside.
    for (const piece of splitOutsideBraces(span)) {
      const text = piece.trim()
      if (!text || !looksLikeCopy(text)) continue
      out.push({ file, line: at + span.slice(0, span.indexOf(piece)).split('\n').length - 1, text, kind: 'jsx-text' })
    }
  }
  return out
}

/** The parts of a JSX span that are not inside `{…}`. */
function splitOutsideBraces(span: string): string[] {
  const parts: string[] = []
  let depth = 0
  let current = ''
  for (const ch of span) {
    if (ch === '{') {
      if (depth === 0 && current) parts.push(current)
      current = ''
      depth++
    } else if (ch === '}') {
      depth = Math.max(0, depth - 1)
    } else if (depth === 0) {
      current += ch
    }
  }
  if (current) parts.push(current)
  return parts
}
