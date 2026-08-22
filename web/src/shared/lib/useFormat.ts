import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import type { Language } from '@/shared/i18n'
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
 * The formatters, bound to the language on screen.
 *
 * The functions underneath stay pure and take the language as an argument —
 * that is what keeps `format.test.ts` able to test them without React. This
 * binds it once per component instead of asking twenty call sites to remember,
 * and forgetting at one of them is exactly how a card ends up reading "3 days
 * ago" in the middle of a Vietnamese page.
 *
 * `formatDuration` and `formatBytes` are here too although they take no
 * language. They are digits and SI units in either tongue, and leaving them
 * out would mean a component importing from two places to format one card.
 */
export function useFormat() {
  const { i18n } = useTranslation()
  const lang = i18n.language as Language

  return useMemo(
    () => ({
      views: (n: number) => formatViews(n, lang),
      count: (n: number) => formatCount(n, lang),
      subscribers: (n: number) => formatSubscribers(n, lang),
      relative: (iso: string) => formatRelative(iso, lang),
      date: (iso: string) => formatDate(iso, lang),
      duration: formatDuration,
      bytes: formatBytes,
    }),
    [lang],
  )
}
