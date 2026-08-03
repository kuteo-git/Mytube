/**
 * The settings page's calls to the gateway.
 *
 * The API key travels one way only. It is sent when the viewer types a new one
 * and never comes back — the gateway answers with whether one is set and its
 * last four characters, which is enough to recognise and not enough to use.
 */

export interface TranslateConfig {
  baseUrl: string
  model: string
  hasKey: boolean
  keyHint: string
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

export const settingsRepository = {
  async listVoices(): Promise<string[]> {
    const r = await fetch('/api/tts/voices')
    return (await json<{ voices: string[] }>(r)).voices ?? []
  },

  async getTranslateConfig(): Promise<TranslateConfig> {
    return json<TranslateConfig>(await fetch('/api/translate/config'))
  },

  async saveTranslateConfig(input: {
    baseUrl: string
    model: string
    /** Empty means "keep the stored one" — the page cannot see it to resend. */
    apiKey: string
  }): Promise<TranslateConfig> {
    const r = await fetch('/api/translate/config', {
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
    const r = await fetch(`/api/translate/models?${q}`)
    return (await json<{ models: string[] }>(r)).models ?? []
  },

  async testTranslate(input: {
    baseUrl: string
    model: string
    apiKey: string
  }): Promise<TranslateTestResult> {
    const r = await fetch('/api/translate/test', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    })
    return json<TranslateTestResult>(r)
  },
}
