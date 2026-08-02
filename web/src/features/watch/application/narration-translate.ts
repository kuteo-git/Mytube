/**
 * Getting a translator to use the right word for "you".
 *
 * NLLB renders English "you" as "anh" — a form that addresses one man, formally.
 * For a video talking to whoever is watching, "bạn" is the word. Rather than
 * correcting the output, which would also rewrite an "anh" that was meant, the
 * pronouns are swapped for markers the model will carry through untouched and
 * put back afterwards.
 *
 * Contractions are expanded rather than replaced whole, so the verb survives for
 * the model to translate: "you're" → "XXXX are" → "bạn là". Replacing "you're"
 * with a bare marker would lose the "are" and with it the tense.
 */

/** Replace addressee pronouns with markers before sending text to translate. */
export function prepForTranslation(text: string): string {
  return (
    text
      // Contractions first: "you're" contains "you", and replacing that first
      // would leave "XXXX're" for the model to make sense of.
      .replace(/\byou're\b/gi, 'XXXX are')
      .replace(/\byou'll\b/gi, 'XXXX will')
      .replace(/\byou'd\b/gi, 'XXXX would')
      .replace(/\byou've\b/gi, 'XXXX have')
      .replace(/\by'all\b/gi, 'XXXX all')
      // Possessive and reflexive forms before the plain pronoun, for the same
      // reason. \b keeps "young" and "youth" out of it.
      .replace(/\byourself\b/gi, 'YYYY')
      .replace(/\byourselves\b/gi, 'YYYY')
      .replace(/\byours\b/gi, 'YYYY')
      .replace(/\byour\b/gi, 'YYYY')
      .replace(/\byou\b/gi, 'XXXX')
  )
}

/** Put the Vietnamese pronouns back into the translated text. */
export function applyAfterTranslation(translated: string): string {
  return translated.replace(/XXXX/gi, 'bạn').replace(/YYYY/gi, 'của bạn')
}
