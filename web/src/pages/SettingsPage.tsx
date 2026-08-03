import { Eye, EyeOff, Headphones, Languages } from 'lucide-react'
import { useEffect, useState } from 'react'
import {
  useSaveTranslateConfig,
  useTestTranslate,
  useTranslateConfig,
  useTranslateModels,
  useVoices,
} from '@/features/settings/application/queries'
import {
  DEFAULT_VOICE,
  MAX_VOICE_LEVEL,
  loadNarrationAudioPrefs,
  saveNarrationAudioPrefs,
  type NarrationAudioPrefs,
} from '@/features/settings/application/settings-prefs'
import { ModelPicker } from '@/features/settings/ui/ModelPicker'
import {
  SettingRow,
  SettingsSection,
} from '@/features/settings/ui/SettingsSection'
import { SliderRow } from '@/features/settings/ui/SliderRow'

const percent = (v: number) => `${Math.round(v * 100)}%`

export function SettingsPage() {
  return (
    <div className="mx-auto max-w-3xl px-4 py-6 min-[700px]:px-6">
      <h1 className="text-2xl font-bold">Settings</h1>
      <NarrationSettings />
      <TranslationSettings />
    </div>
  )
}

function NarrationSettings() {
  const { data: voices } = useVoices()
  const [prefs, setPrefs] = useState(loadNarrationAudioPrefs)

  // Written on every change, not on a Save button. The player re-reads them and
  // the miniplayer keeps talking while you drag, which is the whole point of
  // putting these on a page you can reach without stopping the video.
  const update = (patch: Partial<NarrationAudioPrefs>) => {
    setPrefs((p) => {
      const next = { ...p, ...patch }
      saveNarrationAudioPrefs(next)
      // Same-tab listeners do not get a storage event; the player is listening
      // for this so a drag is audible immediately rather than on next focus.
      window.dispatchEvent(new Event('yt-narration-audio-prefs'))
      return next
    })
  }

  return (
    <SettingsSection
      icon={<Headphones size={18} />}
      title="Narration"
      description="Open a video and come back here to hear these against it — the player keeps going in the corner."
    >
      <SettingRow label="Voice">
        <select
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"
          value={prefs.voice}
          aria-label="Voice"
          onChange={(e) => update({ voice: e.target.value })}
        >
          {/* The stored voice is listed even if the service did not answer, so
              a synthesiser that is down cannot silently reset the choice. */}
          {!(voices ?? []).includes(prefs.voice) && (
            <option value={prefs.voice}>{prefs.voice}</option>
          )}
          {(voices ?? [DEFAULT_VOICE]).map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>
      </SettingRow>

      <SliderRow
        label="Voice volume"
        value={prefs.voiceLevel}
        max={MAX_VOICE_LEVEL}
        format={percent}
        onChange={(voiceLevel) => update({ voiceLevel })}
        hint="Goes past 100% because synthesised speech is quieter than film audio."
      />

      <SliderRow
        label="Video volume while speaking"
        value={prefs.duckLevel}
        max={1}
        format={percent}
        onChange={(duckLevel) => update({ duckLevel })}
      />
    </SettingsSection>
  )
}

function TranslationSettings() {
  const { data: config } = useTranslateConfig()
  const save = useSaveTranslateConfig()
  const models = useTranslateModels()
  const test = useTestTranslate()

  const [baseUrl, setBaseUrl] = useState('')
  const [model, setModel] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [showKey, setShowKey] = useState(false)

  // Seeded once the server answers. The key is not among the fields it sends —
  // it never leaves the server — so its input starts empty and an empty input
  // means "leave the stored one alone".
  useEffect(() => {
    if (!config) return
    setBaseUrl(config.baseUrl)
    setModel(config.model)
  }, [config])

  const form = { baseUrl, model, apiKey }
  const result = test.data

  return (
    <SettingsSection
      icon={<Languages size={18} />}
      title="Translation"
      description="Where subtitles are translated. Changing the model translates fresh — earlier translations are kept, so switching back costs nothing."
    >
      <SettingRow label="Base URL">
        <input
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"
          value={baseUrl}
          placeholder="http://host:port"
          aria-label="Base URL"
          onChange={(e) => setBaseUrl(e.target.value)}
        />
      </SettingRow>

      <SettingRow
        label="API key"
        hint={
          config?.hasKey
            ? `A key ending ${config.keyHint} is stored. Leave blank to keep it.`
            : 'No key stored yet.'
        }
      >
        <input
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"
          type={showKey ? 'text' : 'password'}
          value={apiKey}
          placeholder={config?.hasKey ? '••••••••' : 'sk-…'}
          aria-label="API key"
          onChange={(e) => setApiKey(e.target.value)}
        />
        <button
          type="button"
          aria-label={showKey ? 'Hide the API key' : 'Show the API key'}
          onClick={() => setShowKey((v) => !v)}
          className="grid h-11 w-11 shrink-0 place-items-center rounded-lg bg-surface-hover text-text-2 transition-colors duration-150 ease-out hover:text-text"
        >
          {showKey ? <EyeOff size={16} /> : <Eye size={16} />}
        </button>
      </SettingRow>

      <SettingRow label="Model">
        <ModelPicker
          value={model}
          models={models.data ?? []}
          loading={models.isPending && models.isPaused === false && !models.data}
          onChange={setModel}
          onRefresh={() => models.mutate({ baseUrl, apiKey })}
        />
      </SettingRow>

      <div className="flex gap-2">
        <button
          type="button"
          onClick={() => test.mutate(form)}
          disabled={test.isPending}
          className="h-11 flex-1 rounded-lg bg-surface-hover text-sm font-medium transition-colors duration-150 ease-out hover:bg-white/15 disabled:opacity-50"
        >
          {test.isPending ? 'Testing…' : 'Test'}
        </button>
        <button
          type="button"
          onClick={() => save.mutate(form)}
          disabled={save.isPending}
          className="h-11 flex-1 rounded-lg bg-invert-bg text-sm font-medium text-invert-text transition-opacity duration-150 ease-out hover:opacity-90 disabled:opacity-50"
        >
          {save.isPending ? 'Saving…' : 'Save'}
        </button>
      </div>

      {models.isError && (
        <p className="text-xs text-brand">Could not load the model list.</p>
      )}
      {models.data && (
        <p className="text-xs text-text-2">{models.data.length} models available.</p>
      )}

      {/* The same sentence every time, so pressing Test on one model and then
          another compares like with like. */}
      {result && (
        <div className="rounded-lg bg-surface-input p-3 text-sm">
          {result.error ? (
            <p className="text-brand">{result.error}</p>
          ) : (
            <>
              <p className="text-xs text-text-2">{result.sample}</p>
              <p className="mt-1">{result.translated}</p>
              <p className="mt-1 text-xs text-text-2">{result.ms} ms</p>
            </>
          )}
        </div>
      )}
      {test.isError && (
        <p className="text-xs text-brand">The test call did not get through.</p>
      )}
      {save.isSuccess && !save.isPending && (
        <p className="text-xs text-text-2">Saved. The next batch uses it.</p>
      )}
    </SettingsSection>
  )
}
