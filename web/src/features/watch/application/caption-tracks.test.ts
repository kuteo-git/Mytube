import { describe, expect, it } from 'vitest'

import { captionTracksFor, isManifestTrack } from './caption-tracks'
import type { SubtitleTrack } from '@/features/catalog/domain/video'

const file = (language: string): SubtitleTrack => ({
  language,
  label: language.toUpperCase(),
  url: `/media/x/${language}.vtt`,
  generated: false,
})

describe('captionTracksFor', () => {
  it('offers a broadcast the track its manifest carries', () => {
    // The reported gap: the gateway answers `liveCaptions: true` for a stream
    // whose master names an `en` SUBTITLES rendition, and a broadcast has no
    // `.vtt` beside it — so the list the CC control is built from was empty and
    // the control was never drawn.
    const tracks = captionTracksFor([], { available: true, language: 'en' })

    expect(tracks).toHaveLength(1)
    expect(tracks[0].language).toBe('en')
    expect(tracks[0].generated).toBe(true)
    expect(isManifestTrack(tracks[0])).toBe(true)
  })

  it('leaves a recorded video alone', () => {
    const subtitles = [file('en'), file('vi')]

    expect(captionTracksFor(subtitles, undefined)).toBe(subtitles)
    expect(captionTracksFor(subtitles, { available: false })).toBe(subtitles)
  })

  it('adds nothing when the broadcast publishes no captions', () => {
    // Sky News, measured: on air with no automatic track at all. A control
    // drawn for it would be one that turns on and shows nothing.
    expect(captionTracksFor([], { available: false, language: '' })).toHaveLength(0)
    expect(captionTracksFor([], { available: true, language: '' })).toHaveLength(0)
  })

  it('does not shadow a language the video already has on disk', () => {
    // A finished transcript beats live ASR, so the file wins and the list does
    // not grow a second EN that means something different from the first.
    const subtitles = [file('en')]
    const tracks = captionTracksFor(subtitles, { available: true, language: 'en' })

    expect(tracks).toHaveLength(1)
    expect(isManifestTrack(tracks[0])).toBe(false)
  })
})
