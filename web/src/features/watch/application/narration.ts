/**
 * Vietnamese TTS narration overlay.
 *
 * Reads cues by fetching the VTT file directly rather than going through the
 * browser's TextTrack API.  React adds <track> children to <video> via the DOM
 * rather than via static HTML, and in some browsers this leaves the backing
 * TextTrack uninitialised — readyState stays undefined no matter what mode you
 * set.  Direct fetch + parse has no such problem.
 */
const TTS_VOICE = 'Ngọc Linh'
const PREFETCH_SEC = 8
const MAX_CONCURRENT = 2
const DEFAULT_SPEED = 1.2 // VieNeu-TTS reads slightly slow; 1.2× sounds natural
const MAX_SPEED = 2.0     // ffmpeg atempo is pitch-preserving — 2.0× is fast but clear

// ---- cache ------------------------------------------------------------------
const _cache = new Map<string, AudioBuffer>()
const _active = new Map<string, Promise<AudioBuffer>>()

async function fetchTTS(ctx: AudioContext, text: string, speed: number): Promise<AudioBuffer> {
  // Cache key must include speed — same text at 1.1× and 1.4× produce
  // different audio from the server (ffmpeg atempo).
  const cacheKey = `${text}@@${speed.toFixed(2)}`
  const c = _cache.get(cacheKey)
  if (c) return c
  const inflight = _active.get(cacheKey)
  if (inflight) return inflight
  while (_active.size >= MAX_CONCURRENT) {
    await Promise.race(_active.values()).catch(() => {})
  }
  const p = (async () => {
    const resp = await fetch('/api/tts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text, voice: TTS_VOICE, speed }),
    })
    if (!resp.ok) throw new Error(`tts ${resp.status}`)
    const buf = await ctx.decodeAudioData(await resp.arrayBuffer())
    _cache.set(cacheKey, buf)
    return buf
  })()
  _active.set(cacheKey, p)
  try { return await p } finally { _active.delete(cacheKey) }
}

// ---- VTT parsing ------------------------------------------------------------

interface CueText { start: number; end: number; text: string }

/** Naive WebVTT timestamp → seconds.  "00:01:23.456" → 83.456 */
function parseVTTTime(raw: string): number {
  const parts = raw.split(':')
  if (parts.length !== 3) return NaN
  const h = parseInt(parts[0], 10)
  const m = parseInt(parts[1], 10)
  const s = parseFloat(parts[2])
  return h * 3600 + m * 60 + s
}

/** Strip WebVTT tags and clean up artefacts the TTS would read aloud:
 *  - <c>, </c>, timestamp tags
 *  - [Âm nhạc], [Cười], [Tiếng gió] — sound-effect descriptions
 *  - >>, ♪, ♫ — music notes and other non-speech glyphs
 *  - HTML entities */
function cleanCueText(raw: string): string {
  let s = raw
    // Remove WebVTT angle-bracket tags: <c>, </c>, <00:00:00.000>
    .replace(/<[^>]+>/g, '')
    // Remove [square-bracket] sound-effect descriptions
    .replace(/\[[^\]]*\]/g, '')
    // Remove leading >> (used for speaker indicators in some formats)
    .replace(/^>>\s*/gm, '')
    // Strip music notes and other non-speech symbols the TTS would spell out
    .replace(/[♪♫♬→←↑↓↔«»""''„‚]/g, '')
    // HTML entities
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .trim()

  // Collapse multiple spaces into one.
  s = s.replace(/\s{2,}/g, ' ')

  return s
}

/**
 * Fetch a VTT file, parse it, and return deduplicated cues.
 *
 * YouTube's auto-caption format emits each line twice: once as a long cue with
 * per-character timing tags and once as a ~10 ms clean copy.  After stripping
 * tags both have the same text, so we keep only the *last* occurrence per
 * unique text — it carries the clean text and the longer cue's timing range
 * has already been covered by the previous one.  Overlaps would schedule the
 * same TTS buffer at two slightly different times, creating a chorus effect.
 */
async function fetchAndParseVTT(url: string): Promise<CueText[]> {
  const resp = await fetch(url)
  if (!resp.ok) throw new Error(`VTT ${resp.status}`)
  const raw = await resp.text()

  const cues: CueText[] = []
  const lines = raw.split('\n')
  let i = 0

  // Skip past the header block.
  while (i < lines.length && !lines[i].includes('-->')) i++

  while (i < lines.length) {
    const timingLine = lines[i]
    if (!timingLine || !timingLine.includes('-->')) { i++; continue }

    const arrowIdx = timingLine.indexOf('-->')
    const startRaw = timingLine.substring(0, arrowIdx).trim()
    const rest = timingLine.substring(arrowIdx + 3).trim()
    const endRaw = rest.split(/\s/)[0]

    const start = parseVTTTime(startRaw)
    const end = parseVTTTime(endRaw)
    i++

    if (!isFinite(start) || !isFinite(end)) continue

    const payloadLines: string[] = []
    while (i < lines.length && lines[i].trim() !== '') {
      payloadLines.push(lines[i])
      i++
    }
    i++

    // YouTube tagged cues carry the previous clean sentence on line 1 and the
    // new tagged sentence on line 2.  Joining both would repeat every word
    // ("Xin chào… Xin chào…Xin chúc mừng…").  Only the last line — the one
    // with <c> tags — contains the words this cue actually adds.
    const rawText = payloadLines[payloadLines.length - 1] || ''
    const text = cleanCueText(rawText)
    if (!text) continue

    // Skip YouTube's ~10 ms clean-snapshot cues.  They carry the same text as
    // the preceding tagged cue but with a near-zero timespan; scheduling both
    // would play the same TTS buffer at two slightly different times.
    if (end - start < 0.1) continue

    cues.push({ start, end, text })
  }

  // YouTube emits progressively-accumulating cues where each tagged cue
  // repeats all previous words plus one new phrase.  Strip the shared prefix
  // so each cue speaks only what it adds.
  //
  // The threshold avoids false positives: two unrelated sentences may happen
  // to start with the same word ("Xin chào…" / "Xin chúc mừng…"), but a true
  // carry-over repeats most of the earlier sentence.
  for (let j = cues.length - 1; j >= 1; j--) {
    const prev = cues[j - 1]
    const cur = cues[j]
    let k = 0
    while (k < prev.text.length && k < cur.text.length && prev.text[k] === cur.text[k]) k++
    // Only strip when the overlap is substantial — at least half the previous
    // cue's text.  "Xin " (4 chars) preceding a new sentence is coincidence;
    // "Chào mọi người, tôi là Lương Dũng Nhân," (40 chars) is a true overlap.
    if (k < prev.text.length * 0.5) continue
    // Back up to the last full word boundary.
    while (k > 1 && cur.text[k - 1] !== ' ') k--
    const suffix = cur.text.slice(k).trim()
    if (!suffix) {
      cues.splice(j, 1)
    } else {
      cues[j] = { start: cur.start, end: cur.end, text: suffix }
    }
  }

  return cues
}

// ---- scheduling -------------------------------------------------------------

/** Cues loaded from the VTT file, or null while loading. */
let _cues: CueText[] | null = null
let _cuesURL = ''
let _cuesPromise: Promise<void> | null = null
const _played = new Set<number>()
let _lastLog = 0

// Track the previous clip so we can duck it when a new one starts before it ends.
let _prevEnd = 0           // ctx.currentTime when the last scheduled clip ends
let _prevGain: GainNode | null = null

// Active sources — so we can stop them when the video is paused.
const _activeSources = new Set<AudioBufferSourceNode>()

// Master gain that follows the video element's volume so the TTS narration
// rises and falls with the video audio rather than sitting at a fixed level.
let _masterGain: GainNode | null = null
let _hadCues = false
let _lastTime = 0

/**
 * Kick off a VTT fetch + parse.  Safe to call multiple times — subsequent
 * calls for the same URL are no-ops; a new URL resets state.
 */
export function loadViSubtitles(url: string) {
  if (url === _cuesURL && _cues !== null) return
  _cuesURL = url
  _cues = null
  _played.clear()
  _hadCues = false
  _lastTime = 0
  _prevEnd = 0
  _prevGain = null
  _masterGain = null
  _activeSources.clear()
  _cuesPromise = fetchAndParseVTT(url)
    .then((cues) => {
      _cues = cues
      if (cues.length > 0) {
        console.log('[narration] VTT parsed —', cues.length, 'cues, first:', cues[0]?.text?.slice(0, 60))
      } else {
        console.warn('[narration] VTT parsed but no cues found')
      }
    })
    .catch((err) => {
      console.warn('[narration] VTT fetch/parse failed:', err)
      _cues = []
    })
}

export function tickNarration(video: HTMLVideoElement, ctx: AudioContext) {
  const now = video.currentTime
  const cues = _cues ?? []

  // FIXME: pause is unreliable — sources already scheduled via src.start(when)
  // may still fire, and _activeSources tracking has race conditions with the
  // async TTS fetch.  Ideally the Player should call ctx.suspend() / ctx.resume()
  // from its own pause/play handlers, not from inside the rAF tick.
  if (video.paused) {
    for (const src of _activeSources) {
      try { src.stop() } catch { /* already stopped */ }
    }
    _activeSources.clear()
    return
  }

  // Lazy-init + sync master gain to the video volume so the TTS narration
  // tracks the viewer's volume control instead of playing at a fixed level.
  if (!_masterGain) {
    _masterGain = ctx.createGain()
    _masterGain.connect(ctx.destination)
  }
  // TTS at 2× video volume so the voice cuts through even when ducked.
  _masterGain.gain.setValueAtTime(video.volume * 2, ctx.currentTime)

  // When the viewer seeks backward, unmark cues that are now in the future
  // so they can be played again.
  if (now < _lastTime - 0.5) {
    let unmarked = 0
    for (const idx of _played) {
      const cue = cues[idx]
      if (cue && now <= cue.end + 1) { _played.delete(idx); unmarked++ }
    }
    if (unmarked) console.log('[narration] seek backward — unmarked %d cues', unmarked)
  }
  _lastTime = now

  const t = Date.now()
  if (t - _lastLog > 2000) {
    _lastLog = t
    console.log('[narration] cues=%d played=%d ctx=%s url=%s',
      cues.length, _played.size, ctx.state,
      _cuesURL ? 'loaded' : (_cuesPromise ? 'loading' : 'none'))
  }

  if (!_hadCues && cues.length > 0) {
    _hadCues = true
    console.log('[narration] first cues available — count:', cues.length, 'first text:', cues[0]?.text?.slice(0, 60))
  }

  for (let i = 0; i < cues.length; i++) {
    if (_played.has(i)) continue
    const { start, end, text } = cues[i]

    // Past this cue — skip.
    if (now > end + 1) { _played.add(i); continue }

    // Too far ahead — wait.
    if (start - now > PREFETCH_SEC) continue

    _played.add(i)
    const slot = Math.max(0.1, end - start)

    // Two-pass speed fitting via server-side ffmpeg atempo (pitch-preserving):
    //  1. Fetch at DEFAULT_SPEED (1.1×) — cached per text+speed.
    //  2. If the result is still too long, re-fetch at a faster tempo
    //     (up to MAX_SPEED) so the clip fits its time window.
    //  3. The browser plays at 1.0× — no chipmunk, no overlap.
    ;(async () => {
      let buf = await fetchTTS(ctx, text, DEFAULT_SPEED)
      if (video.paused) return // user paused while TTS was fetching
      if (buf.duration > slot) {
        const needed = Math.min(buf.duration / slot, MAX_SPEED)
        if (needed > DEFAULT_SPEED + 0.02) {
          buf = await fetchTTS(ctx, text, needed)
          if (video.paused) return
        }
      }

      if (ctx.state === 'suspended') {
        console.log('[narration] resuming suspended AudioContext')
        ctx.resume()
      }

      const src = ctx.createBufferSource()
      src.buffer = buf

      const dur = buf.duration
      const when = ctx.currentTime + Math.max(0, start - video.currentTime)
      const clipEnd = when + dur

      // If the previous clip is still playing when this one starts, quickly
      // fade it out so the two don't stack on top of each other.
      if (_prevGain && when < _prevEnd) {
        const duckSec = 0.08
        _prevGain.gain.setValueAtTime(1, when)
        _prevGain.gain.linearRampToValueAtTime(0, when + duckSec)
      }

      const fadeSec = 0.05
      const gain = ctx.createGain()
      gain.gain.setValueAtTime(0, when)
      gain.gain.linearRampToValueAtTime(1, when + Math.min(fadeSec, dur / 2))
      gain.gain.setValueAtTime(1, when + Math.max(0, dur - fadeSec))
      gain.gain.linearRampToValueAtTime(0, when + dur)
      src.connect(gain)
      gain.connect(_masterGain!)

      src.start(when)
      _activeSources.add(src)
      src.onended = () => { _activeSources.delete(src) }

      _prevEnd = Math.max(_prevEnd, clipEnd)
      _prevGain = gain
    })().catch((err) => {
      console.warn('[narration] TTS fetch/play error:', err)
    })
  }
}

export function resetNarration() {
  _played.clear()
  _cues = null
  _cuesURL = ''
  _cuesPromise = null
  _hadCues = false
  _lastTime = 0
  _prevEnd = 0
  _prevGain = null
  _masterGain = null
  _activeSources.clear()
}

/** Whether TTS is currently speaking (cues loaded and being played). */
export function isNarrationActive(): boolean {
  return _cues !== null && _cues.length > 0 && _played.size > 0
}

export function hasVietnameseSubs(video: HTMLVideoElement): boolean {
  for (let i = 0; i < video.textTracks.length; i++) {
    const t = video.textTracks[i]
    if (t.language === 'vi' || t.language === 'vie') return true
  }
  return false
}
