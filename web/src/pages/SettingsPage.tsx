import {
  Activity,
  Bookmark,
  ChevronRight,
  Eye,
  EyeOff,
  HardDrive,
  Headphones,
  Languages,
  LayoutGrid,
  KeyRound,
  SlidersHorizontal,
  UserRound,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
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
import { AdvancedSettings } from '@/features/settings/ui/AdvancedSettings'
import { FeedMixSettings } from '@/features/settings/ui/FeedMixSettings'
import { usePlayer } from '@/features/watch/application/player-context'
import { ModelPicker } from '@/features/settings/ui/ModelPicker'
import { ActionBar } from '@/features/settings/ui/ActionBar'
import { ProfileSettings } from '@/features/identity/ui/ProfileSettings'
import { YouTubeAccountSettings } from '@/features/settings/ui/YouTubeAccountSettings'

import {
  SettingRow,
  SettingsSection,
} from '@/features/settings/ui/SettingsSection'
import { SliderRow } from '@/features/settings/ui/SliderRow'

const percent = (v: number) => `${Math.round(v * 100)}%`

export function SettingsPage() {
  const { isMobile } = usePlayer()

  // A phone gets a list of rows, each opening a screen of its own; a desktop
  // gets the panels themselves, laid out down the page.
  //
  // Not a preference. Three panels of sliders on a 390px column is a page you
  // scroll through looking for the one control you came for, and the thing you
  // came for is never the one on top. A row you tap is the shape a phone uses
  // for exactly this, and it means each screen is only about one thing.
  if (isMobile) {
    return (
      <div className="px-4 py-6">
        <h1 className="text-2xl font-bold">Settings</h1>
        <PhoneMenu />
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-6 min-[700px]:px-6">
      <h1 className="text-2xl font-bold">Settings</h1>
      {/* First, because it decides whose every other setting on this page is. */}
      <ProfileSettings />
      <YouTubeAccountSettings />
      <FeedMixSettings />
      <NarrationSettings />
      <TranslationSettings />
      {/* Last, because it is the one section you arrive at having decided to
          change how the ranking behaves rather than what the page contains. */}
      <AdvancedSettings />
    </div>
  )
}

/**
 * Everything Settings leads to on a phone, as one list.
 *
 * The first three are pages with nowhere else to be reached from: the bottom
 * bar holds at most five entries and what earns a place there is what you move
 * *between* while browsing, so Saved, Storage and Activity land here. None has
 * another way in — Storage's banner appears only above 75% full and can be
 * dismissed, Activity's bell lives on the desktop header, and Saved has nothing
 * else at all.
 *
 * The last three are the settings themselves, each on its own screen rather
 * than stacked down this one. Saved leads because it is the only entry here
 * that is content; the rest are read when you have decided to change or check
 * something.
 */
const PHONE_LIBRARY = [
  { to: '/saved', icon: Bookmark, label: 'Saved' },
  { to: '/storage', icon: HardDrive, label: 'Storage' },
  { to: '/activity', icon: Activity, label: 'Activity' },
]

const PHONE_PREFS = [
  { to: '/settings/profile', icon: UserRound, label: 'Profile' },
  { to: '/settings/youtube-account', icon: KeyRound, label: 'YouTube account' },
  { to: '/settings/feed', icon: LayoutGrid, label: 'Home feed' },
  { to: '/settings/narration', icon: Headphones, label: 'Narration' },
  { to: '/settings/translation', icon: Languages, label: 'Translation' },
  { to: '/settings/advanced', icon: SlidersHorizontal, label: 'Advanced' },
]

function PhoneMenu() {
  return (
    <nav className="mt-4 flex flex-col gap-6" aria-label="Settings">
      <MenuGroup label="Library" items={PHONE_LIBRARY} />
      <MenuGroup label="Preferences" items={PHONE_PREFS} />
    </nav>
  )
}

function MenuGroup({
  label,
  items,
}: {
  label: string
  items: Array<{ to: string; icon: typeof Bookmark; label: string }>
}) {
  return (
    <div>
      <h2 className="px-1 text-xs font-medium uppercase tracking-wide text-text-2">
        {label}
      </h2>
      <div className="mt-1">
        {items.map(({ to, icon: Icon, label }) => (
          <Link
            key={to}
            to={to}
            className="flex items-center gap-3 rounded-xl px-1 py-3.5 text-sm
                       transition-colors hover:bg-surface-hover"
          >
            <Icon size={18} className="shrink-0 text-text-2" />
            <span className="flex-1">{label}</span>
            <ChevronRight size={18} className="shrink-0 text-text-2" />
          </Link>
        ))}
      </div>
    </div>
  )
}

export function NarrationSettings({ headless = false }: { headless?: boolean } = {}) {
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
      headless={headless}
      icon={<Headphones size={18} />}
      title="Narration"
      description="Open a video and come back here to hear these against it — the player keeps going in the corner."
    >
      <div className="rounded-lg bg-surface-input p-3">
        <h3 className="mb-3 text-xs font-medium uppercase tracking-wide text-text-2">Audio</h3>
        <div className="flex flex-col gap-4">
          <SettingRow label="Voice">
            <select
              className="min-w-0 flex-1 rounded-lg border border-line bg-surface-input px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"
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
        </div>
      </div>
    </SettingsSection>
  )
}

export function TranslationSettings({ headless = false }: { headless?: boolean } = {}) {
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
      headless={headless}
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

      <ActionBar>
        <button
          type="button"
          onClick={() => test.mutate(form)}
          disabled={test.isPending}
          className="h-11 rounded-lg bg-surface-hover px-5 text-sm font-medium transition-colors duration-150 ease-out hover:bg-white/15 disabled:opacity-50"
        >
          {test.isPending ? 'Testing…' : 'Test'}
        </button>
        <button
          type="button"
          onClick={() => save.mutate(form)}
          disabled={save.isPending}
          className="h-11 rounded-lg bg-invert-bg px-5 text-sm font-medium text-invert-text transition-opacity duration-150 ease-out hover:opacity-90 disabled:opacity-50"
        >
          {save.isPending ? 'Saving…' : 'Save'}
        </button>
      </ActionBar>

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
