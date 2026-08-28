import { apiFetch } from '@/shared/api/http'
/**
 * The settings page's calls to the gateway.
 *
 * The API key travels one way only. It is sent when the viewer types a new one
 * and never comes back — the gateway answers with whether one is set and its
 * last four characters, which is enough to recognise and not enough to use.
 */

import type { FeedMix, FixedShares } from '@/features/settings/domain/feed-mix'
import type { RankingSettings } from '@/features/settings/domain/ranking'

/**
 * The saved mix, with the defaults the server would fall back to.
 *
 * The defaults come down the wire rather than being written into the page a
 * second time, so "Reset to default" and the note explaining what the default
 * is cannot disagree with what the gateway actually does.
 */
export interface StoredFeedMix extends FeedMix {
  defaults: FeedMix
  /**
   * The shares the sliders do not divide.
   *
   * Sent rather than assumed, so the readout beside each slider cannot go on
   * quoting a figure that stopped being true when a new fixed share appeared.
   */
  fixedShares: FixedShares
}

/**
 * How many videos each share currently has to choose from, keyed by slot name.
 *
 * A separate call from the mix itself. This one is a full ranking pass, and
 * folding it into the 147-byte file read would make the sliders wait on it.
 */
export type BucketSizes = Record<string, number>

export interface TranslateConfig {
  baseUrl: string
  model: string
  hasKey: boolean
  keyHint: string
}

/**
 * Where captions can be asked for when YouTube is refusing this address.
 *
 * The same three fields and the same key rule as TranslateConfig: the key is
 * stored on the server and never sent back, so the browser is told only whether
 * one exists and which one it is.
 *
 * An empty `baseUrl` is the ordinary state and means "do not ask anybody else".
 */
export interface TranscriptConfig {
  baseUrl: string
  hasKey: boolean
  keyHint: string
}

/**
 * What one test fetch produced.
 *
 * The language, the count and the first line rather than a verdict — a server
 * can answer 200 with an empty transcript or with the wrong language, and a
 * status code calls both of those success. The first line is the part a person
 * can look at and know.
 */
export interface TranscriptTestResult {
  language?: string
  generated?: boolean
  cues?: number
  firstLine?: string
  ms?: number
  error?: string
}

export interface TranslateTestResult {
  sample?: string
  translated?: string
  ms?: number
  error?: string
}

async function json<T>(resp: Response): Promise<T> {
  if (!resp.ok) throw new Error(await resp.text())
  return (await resp.json()) as T
}

/**
 * What the server will tell the browser about the synthesiser.
 *
 * Never the key itself — only whether one is stored and its last four
 * characters, which is enough to recognise it by and not enough to use.
 */
export interface TTSConfig {
  baseUrl: string
  model: string
  voice: string
  hasKey: boolean
  keyHint: string
}

/**
 * What a test run produced.
 *
 * `audio` is the clip itself, because "did it work" is not something a status
 * code answers for speech: an endpoint can return 200 and perfectly formed
 * silence. The only test that means anything is hearing it.
 */
export interface TTSTestResult {
  sample?: string
  ms?: number
  bytes?: number
  audio?: string
  error?: string
}

export const settingsRepository = {
  async testTTS(input: {
    baseUrl: string
    model: string
    apiKey: string
    voice: string
  }): Promise<TTSTestResult> {
    return json<TTSTestResult>(
      await apiFetch('/api/settings/tts/test', {
        method: 'POST',
        body: JSON.stringify(input),
      }),
    )
  },

  async getTTSConfig(): Promise<TTSConfig> {
    return json<TTSConfig>(await apiFetch('/api/settings/tts'))
  },

  async saveTTSConfig(input: {
    baseUrl: string
    model: string
    apiKey: string
    voice: string
  }): Promise<TTSConfig> {
    return json<TTSConfig>(
      await apiFetch('/api/settings/tts', {
        method: 'POST',
        body: JSON.stringify(input),
      }),
    )
  },

  async getBucketSizes(): Promise<BucketSizes> {
    const r = await apiFetch('/api/settings/feed-mix/buckets')
    return (await json<{ buckets: BucketSizes }>(r)).buckets ?? {}
  },

  async getRanking(): Promise<RankingSettings> {
    const r = await apiFetch('/api/settings/ranking')
    return (await json<{ settings: RankingSettings }>(r)).settings ?? {}
  },

  async saveRanking(settings: RankingSettings): Promise<RankingSettings> {
    const r = await apiFetch('/api/settings/ranking', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(settings),
    })
    return (await json<{ settings: RankingSettings }>(r)).settings ?? {}
  },

  async getTranslateConfig(): Promise<TranslateConfig> {
    return json<TranslateConfig>(await apiFetch('/api/translate/config'))
  },

  async saveTranslateConfig(input: {
    baseUrl: string
    model: string
    /** Empty means "keep the stored one" — the page cannot see it to resend. */
    apiKey: string
  }): Promise<TranslateConfig> {
    const r = await apiFetch('/api/translate/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    return json<TranslateConfig>(r)
  },

  async listModels(baseUrl: string, apiKey: string): Promise<string[]> {
    const q = new URLSearchParams()
    if (baseUrl) q.set('baseUrl', baseUrl)
    if (apiKey) q.set('apiKey', apiKey)
    const r = await apiFetch(`/api/translate/models?${q}`)
    return (await json<{ models: string[] }>(r)).models ?? []
  },

  async testTranslate(input: {
    baseUrl: string
    model: string
    apiKey: string
  }): Promise<TranslateTestResult> {
    const r = await apiFetch('/api/translate/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    return json<TranslateTestResult>(r)
  },

  async getTranscriptConfig(): Promise<TranscriptConfig> {
    return json<TranscriptConfig>(await apiFetch('/api/settings/transcript'))
  },

  async saveTranscriptConfig(input: {
    baseUrl: string
    apiKey: string
    // An empty base URL is how the household turns this off, so it has to be
    // told apart from "the field was not touched" — which is what an empty
    // string means for every other field on this form.
    clearBaseUrl?: boolean
  }): Promise<TranscriptConfig> {
    const r = await apiFetch('/api/settings/transcript', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    return json<TranscriptConfig>(r)
  },

  async testTranscript(input: {
    baseUrl: string
    apiKey: string
    videoId: string
  }): Promise<TranscriptTestResult> {
    const q = input.videoId ? `?video=${encodeURIComponent(input.videoId)}` : ''
    const r = await apiFetch(`/api/settings/transcript/test${q}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ baseUrl: input.baseUrl, apiKey: input.apiKey }),
    })
    return json<TranscriptTestResult>(r)
  },

  async getFeedMix(): Promise<StoredFeedMix> {
    return json<StoredFeedMix>(await apiFetch('/api/settings/feed-mix'))
  },

  async saveFeedMix(mix: FeedMix): Promise<FeedMix> {
    const r = await apiFetch('/api/settings/feed-mix', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(mix),
    })
    return json<FeedMix>(r)
  },
}
