import type { SubtitleTrack } from '@/features/catalog/domain/video'

/**
 * Which caption tracks a video can offer, and where each one comes from.
 *
 * Two places, and the difference decides who draws the words. A recorded
 * video's captions are `.vtt` files beside it on disk, so they arrive as
 * `subtitles` with a URL and the player mounts a `<track>` for each. A
 * broadcast's are *inside the HLS manifest* — the gateway names a
 * `#EXT-X-MEDIA:TYPE=SUBTITLES` rendition in the master it writes — so there is
 * no file to fetch and nothing for this app to mount. hls.js already has that
 * rendition; what was missing is any way to ask for it.
 *
 * The consequence, measured on the running server before this existed: CNN
 * Headlines answers `liveCaptions: true` with an `en` rendition on all six
 * ladders and segments served as `text/vtt`, and the web showed no CC button at
 * all — because the button is drawn from `subtitles`, which a broadcast never
 * fills. The mobile client had the same fault for the same reason and it is
 * fixed there the same way: put the broadcast's track into the list the control
 * is built from, and mark it as one the *player* renders.
 */

/** A track with no URL is one the player already has. @see captionTracksFor */
export function isManifestTrack(track: SubtitleTrack): boolean {
  return track.url === ''
}

/**
 * What the stream answer says about a broadcast's own captions.
 *
 * Both fields are optional on the wire and absent for everything that is not a
 * broadcast, which is why this is a shape of its own rather than two arguments
 * every caller has to remember the order of.
 */
export interface LiveCaptions {
  available?: boolean
  language?: string
}

/**
 * The tracks to offer, side-loaded first.
 *
 * Side-loaded first because that is the order they are already in and a
 * broadcast has none of them — the concatenation only ever appends to an empty
 * list in practice, and putting the manifest track last keeps it true for a
 * recording that somehow has both.
 *
 * A language the video already carries is not added twice: the file on disk is
 * the better of the two, being a finished transcript rather than live ASR.
 */
export function captionTracksFor(
  subtitles: SubtitleTrack[],
  live: LiveCaptions | undefined,
): SubtitleTrack[] {
  const language = live?.language ?? ''
  if (!live?.available || language === '') return subtitles
  if (subtitles.some((track) => track.language === language)) return subtitles
  return [
    ...subtitles,
    {
      language,
      label: language.toUpperCase(),
      // Empty, and load-bearing: it is what tells the player to select the
      // rendition rather than fetch a file. @see isManifestTrack
      url: '',
      // Live captions are automatic speech recognition, which is exactly what
      // this flag says everywhere else it is set.
      generated: true,
    },
  ]
}
