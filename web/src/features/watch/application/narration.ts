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
const PREFETCH_SEC = 10
const MAX_CONCURRENT = 2
const DEFAULT_SPEED = 1.1 // VieNeu-TTS reads slightly slow; 1.1× sounds natural
const MAX_SPEED = 2.0     // ffmpeg atempo is pitch-preserving — 2.0× is fast but clear
const GAP_BETWEEN_CLIPS = 0.25 // pause between sentences (seconds)

// ---- cache ------------------------------------------------------------------
const _cache = new Map<string, AudioBuffer>()
const _active = new Map<string, Promise<AudioBuffer>>()
const _tlCache = new Map<string, string>() // EN text → VI translation

async function fetchTTS(ctx: AudioContext, text: string, speed: number): Promise<AudioBuffer> {
  // Translate EN → VI when the source subtitle is English.
  let viText = text
  if (_sourceLang === 'en') {
    const cached = _tlCache.get(text)
    if (cached) { viText = cached }
    else {
      const resp = await fetch('/api/translate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text, src: 'eng_Latn', tgt: 'vie_Latn' }),
      })
      if (resp.ok) {
        const { translated } = await resp.json() as { translated: string }
        if (translated) { _tlCache.set(text, translated); viText = translated }
      }
    }
  }

  const cacheKey = `${viText}@@${speed.toFixed(2)}`
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
      body: JSON.stringify({ text: viText, voice: TTS_VOICE, speed }),
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

/** Strip WebVTT tags and clean up artefacts the TTS would read aloud.
 *
 *  [cười], [thở dài], [hắng giọng] are KEPT — the new VieNeu-TTS server
 *  on port 8002 handles emotion tags natively (sigh, laugh, throat-clear).
 *  Other [square-bracket] descriptions like [Âm nhạc], [Tiếng gió] are
 *  still removed because they are not speech. */
function cleanCueText(raw: string): string {
  const EMOTION_TAGS = /\[(cười|thở dài|hắng giọng)\]/gi
  const placeholders: string[] = []

  // Save emotion tags before cleaning.
  let s = raw.replace(EMOTION_TAGS, (match) => {
    placeholders.push(match)
    return `\x00${placeholders.length - 1}\x00`
  })

  // Decode HTML entities first so &gt;&gt; → >> is caught by the >> removal below.
  s = s
    .replace(/&amp;/g, '&')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")

  s = s
    // Remove WebVTT angle-bracket tags: <c>, </c>, <00:00:00.000>
    .replace(/<[^>]+>/g, '')
    // Remove [square-bracket] descriptions — including malformed ones where
    // the closing bracket is missing (YouTube auto-captions often have these).
    .replace(/\[[^\]]*\]?/g, '')
    // Remove >> speaker indicators anywhere in the text.
    .replace(/>>\s*/g, '')
    // Strip music notes and other non-speech symbols the TTS would spell out
    .replace(/[♪♫♬→←↑↓↔«»""''„‚]/g, '')
    .trim()

  // Collapse multiple spaces into one.
  s = s.replace(/\s{2,}/g, ' ')

  // Restore emotion tags.
  s = s.replace(/\x00(\d+)\x00/g, (_, i) => placeholders[+i] || '')

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

  // Group consecutive cues until a sentence/clause boundary so the
  // Split cues at clause/sentence boundaries for cleaner TTS pacing.
  if (_sourceLang === 'en' || _sourceLang === 'vi') {
    // Only treat . ! ? as sentence-ending when preceded by a letter,
    // not a digit (avoids splitting on "2.5", "3.14", etc.).
    // Accumulate cues and split at every . ! ? , boundary.  Each clause
    // keeps the original cue timing: start of first cue, end of last cue
    // that contributed text to this clause.
    //
    // The boundary check excludes number.decimal patterns like "2.5" or
    // "version 3.0" — the period there is not a clause boundary.
    const grouped: CueText[] = []
    let buf = ''
    let bufStart = 0
    let bufEnd = 0
    for (let j = 0; j < cues.length; j++) {
      if (!buf) { bufStart = cues[j].start }
      buf += (buf ? ' ' : '') + cues[j].text
      bufEnd = cues[j].end

      // Find the last clause boundary in buf, skipping digit.digit patterns
      // and commas where the preceding text is too short to stand alone
      // ("Then, how are you" → don't split; "moment, Kimmy" → split).
      let lastEnd = -1
      const re = /[.!?,]/g
      let m: RegExpExecArray | null
      while ((m = re.exec(buf)) !== null) {
        const ch = m[0]
        const idx = m.index
        const before = buf[idx - 1]
        const after = buf[idx + 1]
        // Skip decimal points: "2.5", "3.14"
        if (ch === '.' && before && /\d/.test(before) && after && /\d/.test(after)) continue
        // Clause-ending punctuation must be followed by space or end-of-string.
        if (after && after !== ' ') continue
        // Don't split on comma if either side is too short to stand alone.
        if (ch === ',') {
          const wordsBefore = buf.slice(0, idx).trim().split(/\s+/).length
          const wordsAfter = buf.slice(idx + 1).trim().split(/\s+/).length
          if (wordsBefore <= 2 || wordsAfter <= 2) continue
        }
        lastEnd = idx + 1
      }

      // If no punctuation found but the buffer is getting long, force a split.
      if (lastEnd < 0) {
        const words = buf.trim().split(/\s+/)
        if (words.length >= 15) lastEnd = buf.length
      }

      if (lastEnd > 0) {
        const clause = buf.slice(0, lastEnd).trim()
        if (clause) grouped.push({ start: bufStart, end: bufEnd, text: clause })
        buf = buf.slice(lastEnd).trim()
        bufStart = bufEnd
      }
    }
    if (buf.trim()) {
      grouped.push({ start: bufStart, end: bufEnd, text: buf.trim() })
    }
    cues.length = 0
    cues.push(...grouped)
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
let _sourceLang = 'vi' // 'vi' = cues are Vietnamese, 'en' = need translation
let _skipUntil = -1       // skip all cues before this video time (5s warm-start)
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
export function loadViSubtitles(url: string, lang = 'vi') {
  if (url === _cuesURL && _cues !== null) return
  _cuesURL = url
  _sourceLang = lang
  _cues = null
  _skipUntil = -1
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

  // On first tick with cues, skip the first 5 s so the narration starts
  // smoothly instead of racing to catch up with in-progress cues.
  if (_skipUntil < 0 && cues.length > 0) {
    _skipUntil = now + 10
    console.log('[narration] warm-start — skipping cues before', _skipUntil.toFixed(1) + 's')
  }
  if (_skipUntil > 0) {
    for (let i = 0; i < cues.length; i++) {
      if (cues[i].start < _skipUntil) _played.add(i)
    }
  }

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
  // TTS at 2.5× video volume so the voice cuts through even when ducked.
  _masterGain.gain.setValueAtTime(video.volume * 2.5, ctx.currentTime)

  // When the viewer seeks (forward or backward), stop all currently playing
  // clips, restart the 5 s warm-start window, and unmark future cues.
  if (Math.abs(now - _lastTime) > 0.5) {
    for (const src of _activeSources) {
      try { src.stop() } catch { /* already stopped */ }
    }
    _activeSources.clear()
    _skipUntil = now + 10
    let unmarked = 0
    for (const idx of _played) {
      const cue = cues[idx]
      if (cue && now <= cue.end + 1) { _played.delete(idx); unmarked++ }
    }
    if (unmarked) console.log('[narration] seek — warm-start at', _skipUntil.toFixed(1) + 's, unmarked %d cues', unmarked)
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
      if (video.paused) return
      if (buf.duration > slot) {
        // buf.duration is already sped up by DEFAULT_SPEED (1.2×).  To
        // compute the rate that would bring the *natural* duration down to
        // `slot`, multiply the ratio by DEFAULT_SPEED.
        const natural = buf.duration * DEFAULT_SPEED
        const needed = Math.min(natural / slot, MAX_SPEED)
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
      let when = ctx.currentTime + Math.max(0, start - video.currentTime)
      // Only add a gap when the previous clip has already finished — if
      // they overlap let ducking handle the transition.  This avoids the
      // accumulating drift that would happen if we unconditionally pushed
      // `when` forward.
      if (_prevEnd > 0 && when >= _prevEnd && when < _prevEnd + GAP_BETWEEN_CLIPS) {
        when = _prevEnd + GAP_BETWEEN_CLIPS
      }
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
  _skipUntil = -1
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
