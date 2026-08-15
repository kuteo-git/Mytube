import { describe, expect, it } from 'vitest'
import {
  MACHINE_LANGUAGE,
  captionsSettled,
  desiredTrackMode,
  hasHumanVietnamese,
  subtitleOptions,
} from './subtitle-language'

describe('hasHumanVietnamese', () => {
  it('sees a Vietnamese track the video shipped with', () => {
    expect(hasHumanVietnamese([{ language: 'en' }, { language: 'vi' }])).toBe(true)
    expect(hasHumanVietnamese([{ language: 'vie' }])).toBe(true)
  })

  // The regression. Counting our own output made the translator hide itself
  // the moment it wrote its first batch.
  it('does not count the translation we just produced', () => {
    expect(
      hasHumanVietnamese([{ language: 'en' }, { language: MACHINE_LANGUAGE }]),
    ).toBe(false)
  })

  it('is false when there is nothing Vietnamese at all', () => {
    expect(hasHumanVietnamese([{ language: 'en' }])).toBe(false)
    expect(hasHumanVietnamese([])).toBe(false)
  })
})

describe('captionsSettled', () => {
  // The bug this exists for: the page renders before captions are published,
  // and an empty list answers "no Vietnamese" for a video that has it.
  it('is false before anything has been published', () => {
    expect(captionsSettled([])).toBe(false)
  })

  it('is true once a published track is present', () => {
    expect(captionsSettled([{ language: 'en' }])).toBe(true)
    expect(captionsSettled([{ language: 'en' }, { language: 'vi' }])).toBe(true)
  })

  // Our own output must not be its own evidence: the pass writes this track, so
  // counting it would let the pass declare the list complete.
  it('does not count the machine translation', () => {
    expect(captionsSettled([{ language: MACHINE_LANGUAGE }])).toBe(false)
  })
})

describe('desiredTrackMode', () => {
  const base = { captions: null as string | null, bar: false, narrationOn: false }

  it('shows only the chosen language', () => {
    expect(desiredTrackMode({ ...base, trackLanguage: 'en', captions: 'en' })).toBe(
      'showing',
    )
    expect(desiredTrackMode({ ...base, trackLanguage: 'vi', captions: 'en' })).toBe(
      'disabled',
    )
  })

  // The reported fault: subtitles switched off, and English appears anyway once
  // the file lands. Off means every track is off, whatever else asked for it.
  it('turns everything off when the preference is Off', () => {
    for (const trackLanguage of ['en', 'vi', MACHINE_LANGUAGE]) {
      expect(desiredTrackMode({ ...base, trackLanguage })).toBe('disabled')
    }
  })

  // Illegible over a 128px picture, and the preference is untouched: it comes
  // back the moment the player is a picture again.
  it('draws nothing in the bar, even for the chosen language', () => {
    expect(
      desiredTrackMode({ trackLanguage: 'en', captions: 'en', bar: true, narrationOn: false }),
    ).toBe('disabled')
  })

  // Disabling a track cancels its VTT load, and the voice reads those cues.
  it('hides rather than disables Vietnamese while reading aloud', () => {
    expect(
      desiredTrackMode({ trackLanguage: 'vi', captions: null, bar: false, narrationOn: true }),
    ).toBe('hidden')
    expect(
      desiredTrackMode({ trackLanguage: 'vie', captions: 'en', bar: false, narrationOn: true }),
    ).toBe('hidden')
    expect(
      desiredTrackMode({ trackLanguage: 'en', captions: null, bar: false, narrationOn: true }),
    ).toBe('disabled')
  })

  it('keeps the chosen language showing while reading aloud', () => {
    expect(
      desiredTrackMode({ trackLanguage: 'vi', captions: 'vi', bar: false, narrationOn: true }),
    ).toBe('showing')
  })
})

describe('subtitleOptions', () => {
  const track = (language: string, generated = false) => ({
    language,
    label: language.toUpperCase(),
    generated,
  })

  // What the "Subtitles" setting is hidden by: a row reading "Off" with nothing
  // to turn on is a dead control.
  it('offers nothing for a video with no subtitles', () => {
    expect(subtitleOptions([])).toEqual([])
  })

  // The same is true of a video whose only captions are in a language this
  // household does not read — the list was there, holding only "Off".
  it('offers nothing when no track is English or Vietnamese', () => {
    expect(subtitleOptions([track('de'), track('ja')])).toEqual([])
  })

  it('lists the tracks the video carries', () => {
    expect(subtitleOptions([track('en'), track('vi')]).map((o) => o.value)).toEqual([
      'en',
      'vi',
    ])
  })

  // Choosing it is what starts the translation, so it has to be choosable
  // before the file it names exists.
  it('offers the machine translation where it could be produced', () => {
    expect(subtitleOptions([track('en')]).map((o) => o.value)).toEqual([
      'en',
      MACHINE_LANGUAGE,
    ])
  })

  it('does not offer a translation of a video that already has Vietnamese', () => {
    expect(subtitleOptions([track('en'), track('vi')]).map((o) => o.value)).not.toContain(
      MACHINE_LANGUAGE,
    )
  })

  it('does not offer a second one once the file has been written', () => {
    const values = subtitleOptions([track('en'), track(MACHINE_LANGUAGE)]).map(
      (o) => o.value,
    )
    expect(values).toEqual(['en', MACHINE_LANGUAGE])
  })

  it('marks an auto-generated track in its hint', () => {
    expect(subtitleOptions([track('en', true)])[0].hint).toBe('EN (auto-generated)')
  })
})
