import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { describe, expect, it } from 'vitest'

/**
 * Nothing in the watch feature may write `currentTime` except `player-seek.ts`.
 *
 * ## Why a source scan and not a lint rule
 *
 * This is the rule that keeps coming back. "Never seek a stream reported
 * `seekable: false`" has been in CLAUDE.md §4 for weeks and was still missing
 * from two of the five places that moved a playhead — and a missing guard here
 * produces no error, no log line and no console message, just a video that
 * never starts. Every occurrence has been found by a person watching a video
 * fail, days later.
 *
 * A rule that is only written down gets forgotten at the next call site. This
 * test is the cheapest thing that cannot forget.
 *
 * oxlint is the linter here and has no `no-restricted-syntax`, so the check is
 * a scan of the source rather than of a syntax tree. Crude on purpose: it costs
 * a few milliseconds, it has no configuration, and it fails on the line that
 * introduces the problem rather than on the video that reveals it.
 *
 * ## If this test fails
 *
 * Do not add the file to the exemptions. Route the write through
 * `seekElement(el, tier, seconds)` and handle the outcome — in particular
 * `refused-not-seekable`, which means the stream has to be *reopened* at the
 * mark (`sourceURL(tier, mark, audioStart)`) rather than moved to it.
 */

// Located from the working directory rather than from `import.meta.url`, which
// under the jsdom environment is an http URL and not a file one. Both roots are
// tried because vitest can be started from the repo or from `web/`, and a guard
// that silently scans an empty directory would pass while proving nothing.
const featureRoot = [
  join(process.cwd(), 'src/features/watch'),
  join(process.cwd(), 'web/src/features/watch'),
].find((path) => existsSync(path))

/** The one module allowed to move a playhead, plus the tests that exercise it. */
const allowed = new Set(['application/player-seek.ts'])

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((entry) => {
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) return sourceFiles(path)
    if (!/\.tsx?$/.test(entry)) return []
    // Tests build fake elements and assert on them; they are not the player.
    if (/\.test\.tsx?$/.test(entry)) return []
    return [path]
  })
}

describe('the playhead has one door', () => {
  it('can find the source it is meant to be scanning', () => {
    // Without this the whole guard degrades to an empty list quietly agreeing
    // with itself, which is the one failure mode a scan like this really has.
    expect(featureRoot).toBeDefined()
    expect(sourceFiles(featureRoot!).length).toBeGreaterThan(20)
  })

  it('is written only by player-seek.ts', () => {
    const offenders = sourceFiles(featureRoot!)
      .filter((path) => !allowed.has(relative(featureRoot!, path)))
      .flatMap((path) =>
        readFileSync(path, 'utf8')
          .split('\n')
          .map((line, i) => ({ line, number: i + 1, path }))
          // `.currentTime = x`, but not `=== ` comparisons or `+=`-style reads.
          .filter(({ line }) => /\.currentTime\s*=[^=]/.test(line))
          .map(({ path: p, number, line }) =>
            `${relative(featureRoot!, p)}:${number}  ${line.trim()}`,
          ),
      )

    expect(
      offenders,
      `These write a playhead directly. Route them through seekElement() from ` +
        `application/player-seek.ts, and act on a 'refused-not-seekable' outcome ` +
        `by reopening the stream at the mark rather than seeking to it.`,
    ).toEqual([])
  })

  /**
   * The scan is only worth having if it can actually see a violation, and a
   * regular expression that matches nothing passes just as quietly as a clean
   * codebase. So the pattern is exercised on both sides.
   */
  it('recognises a write, and is not fooled by a read or a comparison', () => {
    const writes = /\.currentTime\s*=[^=]/
    expect(writes.test('el.currentTime = 42')).toBe(true)
    expect(writes.test('  element.currentTime=mark')).toBe(true)
    expect(writes.test('if (el.currentTime === 0) {')).toBe(false)
    expect(writes.test('const now = el.currentTime')).toBe(false)
  })
})
