import { describe, expect, it, vi } from 'vitest'

import {
  formatBytes,
  formatCount,
  formatDate,
  formatDuration,
  formatRelative,
  formatSubscribers,
  formatViews,
} from './format'

/**
 * The formatters carry more English than anything else in the app, and they
 * carry it where it is hardest to notice — not in a label somebody wrote once,
 * but inside a return value that renders on every card in every grid.
 *
 * They are pure, and these tests are why that is worth keeping: language is an
 * argument, so both languages can be checked here without a React tree, a
 * provider or a rendered component.
 */

describe('counts', () => {
  it('abbreviates with each language own initials', () => {
    // Vietnamese does not use K/M/B. nghìn, triệu, tỷ — which is what YouTube
    // shows there and what anybody reading a view count expects.
    expect(formatCount(4_100_000, 'en')).toBe('4.1M')
    expect(formatCount(4_100_000, 'vi')).toBe('4.1Tr')
    expect(formatCount(2_400, 'vi')).toBe('2.4N')
    expect(formatCount(3_000_000_000, 'vi')).toBe('3T')
  })

  it('leaves a small number alone', () => {
    expect(formatViews(248, 'en')).toBe('248 views')
    expect(formatViews(248, 'vi')).toBe('248 lượt xem')
  })

  it('names subscribers in both', () => {
    expect(formatSubscribers(4_100_000, 'vi')).toBe('4.1Tr người đăng ký')
  })

  /**
   * Billions were unreachable in the count formatter and reachable in the view
   * formatter, which had its own copy of the ladder. Merging them fixed a
   * subscriber count of three billion reading "3000M".
   */
  it('reaches billions through the shared ladder', () => {
    expect(formatCount(3_000_000_000, 'en')).toBe('3B')
  })
})

describe('relative time', () => {
  it('puts the marker where each language puts it', () => {
    const threeDaysAgo = new Date(Date.now() - 3 * 86_400_000).toISOString()
    // Not one shape with a translated word: English marks before the unit and
    // Vietnamese after the phrase. Getting this wrong is what makes a
    // translation read as a machine's.
    expect(formatRelative(threeDaysAgo, 'en')).toBe('3 days ago')
    expect(formatRelative(threeDaysAgo, 'vi')).toBe('3 ngày trước')
  })

  /**
   * The English plural was applied by appending "s" to the unit. Vietnamese has
   * no plural, so that rule carried across produces "3 ngàys trước".
   */
  it('does not invent a Vietnamese plural', () => {
    const twoYearsAgo = new Date(Date.now() - 2 * 31_536_000_000).toISOString()
    expect(formatRelative(twoYearsAgo, 'vi')).toBe('2 năm trước')
    expect(formatRelative(twoYearsAgo, 'en')).toBe('2 years ago')
  })

  it('says just now under a minute, in both', () => {
    const now = new Date().toISOString()
    expect(formatRelative(now, 'en')).toBe('just now')
    expect(formatRelative(now, 'vi')).toBe('vừa xong')
  })
})

describe('dates', () => {
  /**
   * ICU rather than a table of month names: it already knows Vietnamese writes
   * "22 thg 8, 2026", and beating it would mean maintaining a list to arrive at
   * the same answer.
   */
  it('uses each locale own short form', () => {
    expect(formatDate('2026-08-22T00:00:00Z', 'en')).toContain('2026')
    expect(formatDate('2026-08-22T00:00:00Z', 'vi')).toContain('thg')
  })
})

describe('the language-neutral ones', () => {
  it('formats a duration as digits', () => {
    expect(formatDuration(641)).toBe('10:41')
    expect(formatDuration(3_661)).toBe('1:01:01')
  })

  it('formats bytes as SI units', () => {
    expect(formatBytes(1024 ** 3 * 2.5)).toBe('2.5 GB')
  })
})

describe('defaults', () => {
  /**
   * English when nobody says, because English is the source language. Every
   * component goes through useFormat and passes one explicitly; this is only
   * for a caller that has no language to hand.
   */
  it('falls back to English', () => {
    expect(formatViews(100)).toBe('100 views')
    vi.useRealTimers()
  })
})
