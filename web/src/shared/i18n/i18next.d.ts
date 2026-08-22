import type { en } from './en'

/**
 * Teach TypeScript what keys exist.
 *
 * Without this `t` takes any string and a typo renders as the key itself —
 * "nav.setings" on screen, in both languages, reported by nothing. With it the
 * build fails on the line that introduced it.
 *
 * This is the first of the three layers guarding against half-translation. It
 * cannot see a string that was never extracted at all; that is what
 * `untranslated.guard.test.ts` is for.
 */
declare module 'i18next' {
  interface CustomTypeOptions {
    defaultNS: 'translation'
    resources: { translation: typeof en }
  }
}
