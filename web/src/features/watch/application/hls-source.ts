/**
 * Whether this browser can play an HLS playlist by itself, and how to give it
 * one.
 *
 * ## Why this is not one line
 *
 * The obvious test is `canPlayType('application/vnd.apple.mpegurl')`, and it
 * lies. Measured 2026-08-20:
 *
 * | | canPlayType | actually plays master.m3u8 |
 * |---|---|---|
 * | Chrome, macOS | `"maybe"` | **no** — `MEDIA_ERR_SRC_NOT_SUPPORTED` |
 * | Safari, iOS 18.7 | `"maybe"` | **yes** — played, duration 641.8s, seeked twice |
 *
 * The same answer from both, and opposite outcomes. `web/public/mse-check.html`
 * asked exactly that question, and the HLS work was built believing "maybe"
 * meant yes on the strength of it.
 *
 * So the question is asked a different way. What actually distinguishes the two
 * is which media pipeline the browser has: Safari has native HLS, and on iOS it
 * has `ManagedMediaSource` but **no** `MediaSource` at all. Chrome has
 * `MediaSource` and no HLS. That is a property of the engine rather than a
 * claim about a MIME type, and it cannot answer "maybe".
 *
 * ## What this means for the iPhone
 *
 * There is nothing behind native HLS on iOS. hls.js needs `MediaSource`, which
 * iOS does not have, so if Safari declines a playlist the device has no way to
 * play a video that is not on disk yet. That is why the codec string is
 * validated on the server before a playlist is written: this is the one path
 * with no fallback under it.
 */

/**
 * Does this browser play HLS on its own?
 *
 * Read from the engine, not from `canPlayType`. A browser with native HLS and
 * no `MediaSource` is Safari on iOS; one with both is Safari on macOS; one with
 * only `MediaSource` is Chrome or Firefox and needs a library.
 *
 * `ManagedMediaSource` is Apple's, and its presence is the sharpest signal
 * available that this is a WebKit that plays HLS. On macOS Safari, where it may
 * be absent, the vendor check catches it: Safari is the only engine that
 * reports "Apple Computer, Inc." while not being Chrome.
 */
export function canPlayHLSNatively(): boolean {
  if (typeof window === 'undefined') return false

  // Apple's own media source. Present on iOS and on recent Safari, and never on
  // Chrome or Firefox.
  if ('ManagedMediaSource' in window) return true

  const nav = window.navigator
  const isAppleVendor = nav.vendor === 'Apple Computer, Inc.'
  // Chrome on macOS reports Safari in its user agent but not Apple as vendor;
  // Chromium engines are excluded by name for the cases where they do.
  const isChromium = /Chrome|Chromium|Edg\//.test(nav.userAgent)
  return isAppleVendor && !isChromium
}

/**
 * Can this browser be *made* to play HLS?
 *
 * Separate from the question above because the answer costs a library. Nothing
 * here loads it — that is the caller's decision and a later change — but the
 * player has to be able to tell "cannot play this at all" from "cannot play it
 * unaided", because only the first means fall back to the muxed stream.
 */
export function canPlayHLSWithLibrary(): boolean {
  return typeof window !== 'undefined' && 'MediaSource' in window
}

/**
 * Should the player open this video on HLS?
 *
 * Anywhere it can be played at all — unaided on Safari and iOS, through hls.js
 * on anything with `MediaSource`. That covers every browser this is built for
 * (CLAUDE.md §2: phone browsers and desktop), and it is what makes the muxed
 * tier removable: while one browser still needed it, none of the machinery
 * built around an unindexed stream could go.
 */
export function shouldUseHLS(): boolean {
  return canPlayHLSNatively() || canPlayHLSWithLibrary()
}

/**
 * What the browser reports, for the log.
 *
 * The player prints this once per video. It exists because every wrong turn in
 * this area came from believing a capability check instead of an outcome, and
 * the two are only distinguishable afterwards if the claim was written down.
 */
export function hlsCapabilities(): {
  native: boolean
  withLibrary: boolean
  claim: string
} {
  const claim =
    typeof document === 'undefined'
      ? ''
      : document.createElement('video').canPlayType('application/vnd.apple.mpegurl')
  return {
    native: canPlayHLSNatively(),
    withLibrary: canPlayHLSWithLibrary(),
    // Kept only to be disbelieved: "maybe" from both a browser that plays it
    // and one that does not.
    claim,
  }
}

/**
 * Is this URL an HLS playlist?
 *
 * Read from the address rather than carried alongside it, because the player's
 * two layers hold a bare string and every comparison in the tier machinery is
 * made against that string. Giving it a companion field would mean keeping the
 * two in step at each of those sites.
 */
export function isHLSPlaylist(url: string | undefined): boolean {
  return Boolean(url) && url!.includes('.m3u8')
}

/**
 * Does this source have to be attached by hand rather than assigned to `src`?
 *
 * Only where the browser cannot play HLS unaided. Safari and iOS take the
 * playlist as an ordinary `src`, and that path is left exactly as it is —
 * it is the one measured working on the device with no fallback, and there is
 * nothing to gain by routing it through a library instead.
 */
export function needsHLSLibrary(url: string | undefined): boolean {
  return isHLSPlaylist(url) && !canPlayHLSNatively()
}

/**
 * A live attachment: how to change what it is playing, and how to stop it.
 *
 * `selectHeight` is the quality menu made real. Without it the menu is what
 * CLAUDE.md §5 forbids outright — a control that looks like it does something:
 * the height was carried as a label on the tier while the URL it pointed at was
 * the same master playlist either way, so pressing 1080p relabelled a 720p
 * picture and changed nothing.
 */
export interface HLSAttachment {
  detach: () => void
  /**
   * Pin a rendition by height, or `undefined` to let the player choose.
   *
   * A height that is not on the ladder falls back to automatic rather than to
   * the nearest rung: the ladder is what the server resolved for this video,
   * and silently substituting a different picture than the one asked for is how
   * a control stops meaning what it says.
   */
  selectHeight: (height: number | undefined) => void
  /**
   * The ladder this video publishes, heights only, highest first.
   *
   * Reported rather than assumed. Which rungs exist is a property of the video —
   * YouTube does not publish every height for every upload, and above 1080p many
   * publish nothing at all — so a menu built from a constant offers renditions
   * this video does not have, which is §5's dead button wearing a number.
   */
  onLevels: (cb: (heights: number[]) => void) => void
  /**
   * The rung actually on screen, whenever it changes.
   *
   * Under Auto this is the only way to tell "the ladder dropped to 240p because
   * the connection dipped" from "the video is broken", and it is what stops the
   * player climbing down from a 4K stream to a 1080p file on disk.
   */
  onLevelSwitched: (cb: (height: number) => void) => void
}

/**
 * Can a rendition be chosen by hand on this browser?
 *
 * Only through hls.js. Native HLS gives a page no way to pin a level — Safari
 * decides from its own bandwidth estimate and there is no standard API to
 * override it — so on iPhone the honest menu is "Auto" alone. On a LAN that is
 * no loss: the estimate lands on the top rung.
 */
export function canSelectHLSLevel(): boolean {
  return !canPlayHLSNatively() && canPlayHLSWithLibrary()
}

/**
 * Play `url` on `el` using hls.js, and hand back the way to stop.
 *
 * The library is imported here and nowhere else, so it lands in its own chunk
 * and the browsers with native HLS — every phone in this house — never download
 * it.
 *
 * **It is 179 kB gzipped**, measured on the real build, not the ~40 kB this was
 * planned at. Said plainly because the estimate was used to justify the
 * dependency. It is still the right trade — it buys the removal of the mux and
 * everything built around an unindexed stream — but it is four times the price
 * quoted, and it is paid only by desktop browsers.
 *
 * The `hls.light` build is smaller (925 kB raw against 1448 kB) and **cannot be
 * used here**: it drops alternate-audio support, and this master playlist
 * describes audio as an `EXT-X-MEDIA` group precisely because YouTube publishes
 * the two tracks separately. Dropping it would drop the sound.
 *
 * Errors are turned into an `error` event on the element rather than reported
 * separately. The player already knows how to retreat from a source that will
 * not load, and it decides that from the element; a second, parallel way of
 * failing would be a second thing to keep in step with it.
 */
export async function attachHLS(el: HTMLVideoElement, url: string): Promise<HLSAttachment> {
  const { default: Hls } = await import('hls.js')

  if (!Hls.isSupported()) {
    // Nothing can play it here. Said the way the player already listens for.
    el.dispatchEvent(new Event('error'))
    return { detach: () => {}, selectHeight: () => {}, onLevels: () => {}, onLevelSwitched: () => {} }
  }

  const hls = new Hls({
    // The playlist is VOD and the segments are byte ranges into two files that
    // are already on the far side of a proxy; the defaults are tuned for a live
    // edge that does not exist here.
    enableWorker: true,
    lowLatencyMode: false,
  })

  hls.on(Hls.Events.ERROR, (_event, data) => {
    // Only a fatal error is the player's business. hls.js recovers from the
    // rest on its own, and reporting those would retreat from a tier that is
    // still working.
    if (!data.fatal) return
    console.error('[debug] hls.js fatal', data.type, data.details)
    el.dispatchEvent(new Event('error'))
  })

  // What the viewer asked for, remembered until the levels are known.
  //
  // The ladder arrives with the master playlist, which is fetched after this
  // returns — so a height chosen before then has nowhere to be applied yet, and
  // applying it on arrival is the difference between the menu working and the
  // menu working only if you press it twice.
  let wanted: number | undefined
  const apply = () => {
    if (wanted === undefined) {
      hls.currentLevel = -1
      return
    }
    const index = hls.levels.findIndex((l) => l.height === wanted)
    // -1 is automatic, which is the right answer for a height this video does
    // not publish: better the player's own choice than a rendition nobody asked
    // for wearing the label of one they did.
    hls.currentLevel = index
  }
  hls.on(Hls.Events.MANIFEST_PARSED, apply)

  // Both reports are remembered, not merely forwarded. The caller attaches
  // asynchronously — the library has to be fetched first — so hls.js can parse
  // the manifest and settle on a rung before anyone has subscribed. Without
  // replay the readout stayed a bare "Auto" until the ladder happened to move
  // again, which on a good connection is never.
  let levels: number[] | undefined
  let onLevels: ((heights: number[]) => void) | undefined
  hls.on(Hls.Events.MANIFEST_PARSED, () => {
    levels = hls.levels.map((l) => l.height).sort((a, b) => b - a)
    onLevels?.(levels)
  })

  let playing: number | undefined
  let onLevelSwitched: ((height: number) => void) | undefined
  hls.on(Hls.Events.LEVEL_SWITCHED, (_event, data) => {
    const height = hls.levels[data.level]?.height
    if (height === undefined) return
    playing = height
    onLevelSwitched?.(height)
  })

  hls.loadSource(url)
  hls.attachMedia(el)

  return {
    detach: () => hls.destroy(),
    selectHeight: (height) => {
      wanted = height
      if (hls.levels.length > 0) apply()
    },
    onLevels: (cb) => {
      onLevels = cb
      if (levels) cb(levels)
    },
    onLevelSwitched: (cb) => {
      onLevelSwitched = cb
      if (playing !== undefined) cb(playing)
    },
  }
}

/**
 * Does this browser leave the graph empty while playing this source?
 *
 * Measured on iPhone (iOS 18.7, 2026-08-21), one page, one audio graph, one
 * analyser, three sources — the only thing changed between readings was what
 * the element was playing:
 *
 *	ordinary MP4 from disk          signal in graph = 0.0806
 *	HLS played natively             signal in graph = 0.0000
 *	HLS played through hls.js       signal in graph = 0.0000
 *
 * The third reading is the one that settles it. `hls.js` genuinely was the
 * source — the element reported `blob (MSE)`, checked precisely because a
 * silent fall back to native playback would have looked identical and would
 * have made this a measurement of native HLS twice over. So it is not native
 * HLS that Web Audio cannot reach: it is HLS on this platform, by either road,
 * and no library can fix it. `createMediaElementSource` still *succeeds* —
 * nothing throws — which is why the player's existing fallback never fired.
 *
 * The consequence is wider than the equaliser, and worse: every node in the
 * graph is inert for that stretch, so the room, the volume gain and narration's
 * ducking are too. Volume moved off `element.volume` into a gain node
 * deliberately (§5); on a phone mid-download that gain node is attached to
 * nothing, and the slider is a dead control.
 *
 * Reads the URL rather than the tier so the two layers can be asked separately
 * — during a handover they are playing different things.
 */
export function bypassesWebAudio(url: string | undefined): boolean {
  return isHLSPlaylist(url) && canPlayHLSNatively()
}
