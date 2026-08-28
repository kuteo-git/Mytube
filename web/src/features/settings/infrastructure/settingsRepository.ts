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
 * The outbound proxy, and which traffic goes through it.
 *
 * The URL comes back with its password replaced by bullets and everything else
 * intact — see the gateway's maskProxyURL. That is a deliberate softening of
 * the rule the other credentials on this screen follow (never sent back at
 * all): the password lives inside the one field somebody has to be able to
 * read, and blanking the whole thing leaves them unable to tell which provider
 * they configured.
 *
 * Sending the masked URL back on save is normal and expected; the server puts
 * the stored password back where the bullets are.
 */
export interface ProxyConfig {
  url: string
  enabled: boolean
  forCaptions: boolean
  forListings: boolean
  forMedia: boolean
  forComments: boolean
}

/**
 * What one proxy test measured.
 *
 * Three things fail separately and a single verdict answers none of them: the
 * proxy may not carry a request at all, it may carry one and not change the
 * address, and the address may be changed and still refused by YouTube. So both
 * addresses come back, and the outcome of one real caption fetch beside them.
 */
export interface ProxyTestResult {
  directIp?: string
  proxyIp?: string
  /** A code, translated on this side — the server does not know the language. */
  code?: string
  captionsOk: boolean
  captionsLang?: string
  cues?: number
  captionsCode?: string
  tookMs: number
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

  async getProxyConfig(): Promise<ProxyConfig> {
    return json<ProxyConfig>(await apiFetch('/api/settings/proxy'))
  },

  async saveProxyConfig(input: ProxyConfig): Promise<ProxyConfig> {
    const r = await apiFetch('/api/settings/proxy', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    return json<ProxyConfig>(r)
  },

  // Tests the values in the form, not the ones on disk: testing after saving is
  // testing what you have already accepted.
  async testProxy(input: ProxyConfig): Promise<ProxyTestResult> {
    const r = await apiFetch('/api/settings/proxy/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    return json<ProxyTestResult>(r)
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
