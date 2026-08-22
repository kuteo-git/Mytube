import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'

import { en } from './en'
import { vi } from './vi'

/**
 * Two languages, English and Tiếng Việt.
 *
 * English is the source: it is what the code is written in (CLAUDE.md §4b) and
 * what every key's value is authored as. Vietnamese is a translation kept
 * beside it, and the checks in this directory exist to keep the two in step —
 * the failure worth preventing is not a wrong translation, which somebody
 * notices and fixes, but a *missing* one, which renders in English on a screen
 * the viewer asked to be in Vietnamese and reports nothing anywhere.
 */

export const LANGUAGES = ['en', 'vi'] as const
export type Language = (typeof LANGUAGES)[number]

/** Each language named in itself, so somebody in the wrong one can read out. */
export const LANGUAGE_NAMES: Record<Language, string> = {
  en: 'English',
  vi: 'Tiếng Việt',
}

const STORAGE_KEY = 'yt-language-v1'

/**
 * What to open in.
 *
 * A saved choice always wins. Failing that, the browser's own language — this
 * household speaks Vietnamese, and defaulting to English would mean everybody
 * changing it by hand on every device they own. Only the primary subtag is
 * read: `vi-VN` and `vi` are one language, the same rule the feed's language
 * filter already applies.
 *
 * Read synchronously at module load rather than in an effect, so the first
 * paint is already in the right language. An effect would show English for a
 * frame and then swap, which is a worse first impression than either language
 * on its own.
 */
export function initialLanguage(): Language {
  try {
    const saved = localStorage.getItem(STORAGE_KEY)
    if (saved && (LANGUAGES as readonly string[]).includes(saved)) {
      return saved as Language
    }
  } catch {
    // Private browsing, or storage disabled. Not a reason to fail to start.
  }
  const preferred = navigator.language?.split('-')[0]
  return preferred === 'vi' ? 'vi' : 'en'
}

/**
 * Change language everywhere, and remember it.
 *
 * `documentElement.lang` moves with it because that is what a screen reader
 * picks a voice from — left at "en", Vietnamese is read aloud by an English
 * voice, which is unintelligible rather than merely wrong.
 */
export function setLanguage(next: Language) {
  void i18n.changeLanguage(next)
  document.documentElement.lang = next
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // The language still changes for this session; only the memory is lost.
  }
}

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, vi: { translation: vi } },
  lng: initialLanguage(),
  // English, not the device's language: a key missing from Vietnamese must show
  // *something*, and the English original is the only other thing that exists.
  // The dictionary test is what stops that ever being reached.
  fallbackLng: 'en',
  interpolation: {
    // React escapes everything it renders already, and doing it twice turns an
    // apostrophe in "Couldn't copy the link" into an entity on screen.
    escapeValue: false,
  },
})

document.documentElement.lang = i18n.language

export default i18n
