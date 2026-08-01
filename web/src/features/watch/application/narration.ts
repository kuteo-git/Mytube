/**
 * Vietnamese TTS narration overlay.
 */
const TTS_VOICE = 'Ngọc Linh'
const PREFETCH_SEC = 8
const MAX_CONCURRENT = 2

// ---- cache ------------------------------------------------------------------
const _cache = new Map<string, AudioBuffer>()
const _active = new Map<string, Promise<AudioBuffer>>()

async function fetchTTS(ctx: AudioContext, text: string): Promise<AudioBuffer> {
  const c = _cache.get(text)
  if (c) return c
  const inflight = _active.get(text)
  if (inflight) return inflight
  while (_active.size >= MAX_CONCURRENT) {
    await Promise.race(_active.values()).catch(() => {})
  }
  const p = (async () => {
    const resp = await fetch('/api/tts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text, voice: TTS_VOICE }),
    })
    if (!resp.ok) throw new Error(`tts ${resp.status}`)
    const buf = await ctx.decodeAudioData(await resp.arrayBuffer())
    _cache.set(text, buf)
    return buf
  })()
  _active.set(text, p)
  try { return await p } finally { _active.delete(text) }
}

// ---- cue extraction ---------------------------------------------------------

interface CueText { start: number; end: number; text: string }

function viCues(video: HTMLVideoElement): CueText[] {
  const out: CueText[] = []
  for (let i = 0; i < video.textTracks.length; i++) {
    const t = video.textTracks[i]
    if (t.language !== 'vi' && t.language !== 'vie') continue
    if (t.mode === 'disabled') t.mode = 'hidden'
    if (t.readyState !== 2 || !t.cues) continue
    for (let j = 0; j < t.cues.length; j++) {
      const c = t.cues[j] as VTTCue
      const text = (c.text || '').trim()
      if (!text || !isFinite(c.startTime) || !isFinite(c.endTime)) continue
      out.push({ start: c.startTime, end: c.endTime, text })
    }
  }
  return out
}

// ---- scheduling -------------------------------------------------------------
const _played = new Set<number>()
let _lastLog = 0

export function tickNarration(video: HTMLVideoElement, ctx: AudioContext) {
  const now = video.currentTime
  const cues = viCues(video)

  const t = Date.now()
  if (t - _lastLog > 2000) {
    _lastLog = t
    console.log('[narration] cues=', cues.length, 'played=', _played.size, 'ctx=', ctx.state)
  }

  for (let i = 0; i < cues.length; i++) {
    if (_played.has(i)) continue
    const { start, end, text } = cues[i]

    // Past this cue — skip.
    if (now > end + 1) { _played.add(i); continue }

    // Too far ahead — wait.
    if (start - now > PREFETCH_SEC) continue

    _played.add(i)
    fetchTTS(ctx, text).then((buf) => {
      if (ctx.state === 'suspended') ctx.resume()
      const src = ctx.createBufferSource()
      src.buffer = buf
      src.connect(ctx.destination)
      const when = ctx.currentTime + Math.max(0, start - video.currentTime)
      src.start(when, 0, Math.max(0.5, end - start))
    }).catch(() => {})
  }
}

export function resetNarration() { _played.clear() }

export function hasVietnameseSubs(video: HTMLVideoElement): boolean {
  for (let i = 0; i < video.textTracks.length; i++) {
    const t = video.textTracks[i]
    if (t.language === 'vi' || t.language === 'vie') return true
  }
  return false
}
