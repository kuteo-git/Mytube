/**
 * Vietnamese TTS narration overlay.
 *
 * Reads cues from a VTT text track (loaded as <track> on the video element),
 * calls POST /api/tts for each cue, and plays the resulting WAV through the
 * Web Audio API in sync with the video.
 */

const TTS_VOICE = 'Ngọc Linh'

// ---------------------------------------------------------------------------
// Audio cache
// ---------------------------------------------------------------------------

const _cache = new Map<string, Promise<AudioBuffer>>()

function fetchTTS(ctx: AudioContext, text: string): Promise<AudioBuffer> {
  const cached = _cache.get(text)
  if (cached) return cached
  const p = fetch('/api/tts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text, voice: TTS_VOICE }),
  })
    .then((r) => {
      if (!r.ok) throw new Error(`tts ${r.status}`)
      return r.arrayBuffer()
    })
    .then((wav) => ctx.decodeAudioData(wav))
  _cache.set(text, p)
  return p
}

// ---------------------------------------------------------------------------
// Cue extraction from the browser's VTT text track
// ---------------------------------------------------------------------------

interface CueText {
  start: number
  end: number
  text: string
}

function vietnameseCues(video: HTMLVideoElement): CueText[] {
  const cues: CueText[] = []
  for (let i = 0; i < video.textTracks.length; i++) {
    const t = video.textTracks[i]
    if (t.language !== 'vi' && t.language !== 'vie') continue
    if (t.mode === 'disabled') t.mode = 'hidden'
    if (!t.cues) continue
    for (let j = 0; j < t.cues.length; j++) {
      const c = t.cues[j] as VTTCue
      const text = (c.text || '').trim()
      if (!text) continue
      cues.push({ start: c.startTime, end: c.endTime, text })
    }
  }
  return cues
}

// ---------------------------------------------------------------------------
// Scheduling loop (called from requestAnimationFrame)
// ---------------------------------------------------------------------------

const _played = new Set<number>() // cue indices already played/skipped

export function tickNarration(video: HTMLVideoElement, ctx: AudioContext) {
  const now = video.currentTime
  const cues = vietnameseCues(video)

  for (let i = 0; i < cues.length; i++) {
    if (_played.has(i)) continue
    const cue = cues[i]

    // Skip cues already in the past (by >200ms so we don't skip one that
    // just barely became due this tick).
    if (now > cue.endTime + 0.2) {
      _played.add(i)
      continue
    }

    // Not yet due — prefetch and wait.
    if (cue.startTime - now > 0.3) continue

    // This cue is due. Mark it so we never double-fire it.
    _played.add(i)

    fetchTTS(ctx, cue.text)
      .then((buf) => {
        const src = ctx.createBufferSource()
        src.buffer = buf
        src.connect(ctx.destination)
        const delay = Math.max(0, cue.startTime - ctx.currentTime)
        const duration = Math.max(0, cue.endTime - cue.startTime)
        src.start(ctx.currentTime + delay, 0, duration)
      })
      .catch(() => {}) // one bad cue shouldn't break the rest
  }
}

/** Reset played-cue state when a new video loads. */
export function resetNarration() {
  _played.clear()
}

/** Whether the video has a Vietnamese subtitle track. */
export function hasVietnameseSubs(video: HTMLVideoElement): boolean {
  for (let i = 0; i < video.textTracks.length; i++) {
    const t = video.textTracks[i]
    if (t.language === 'vi' || t.language === 'vie') return true
  }
  return false
}
