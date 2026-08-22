import {
  Activity,
  Bookmark,
  ChevronRight,
  Clock,
  Eye,
  EyeOff,
  HardDrive,
  Headphones,
  Languages,
  LayoutGrid,
  ListVideo,
  KeyRound,
  SlidersHorizontal,
  UserRound,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import {} from 'react-router-dom'
import {
  useSaveTranslateConfig,
  useTestTranslate,
  useTranslateConfig,
  useTranslateModels,
  useTTSConfig,
  useSaveTTSConfig,
  useTestTTS,
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
import { useTranslation } from 'react-i18next'
import type { TranslationKey } from '@/shared/i18n/en'
import { PageLink } from '@/shared/ui/PageLink'

const percent = (v: number) => `${Math.round(v * 100)}%`

export function SettingsPage() {
  const { t } = useTranslation()
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
        <h1 className="text-2xl font-bold">{t('ui.settings')}</h1>
        <PhoneMenu />
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-6 min-[700px]:px-6">
      <h1 className="text-2xl font-bold">{t('ui.settings')}</h1>
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
// Keys, not words. These are module constants and cannot call a hook, so text
// baked in here would never change language — the same rule the sidebar's
// arrays follow, for the same reason.
const PHONE_LIBRARY: MenuItem[] = [
  { to: '/saved', icon: Bookmark, label: 'nav.saved' },
  { to: '/storage', icon: HardDrive, label: 'nav.storage' },
  { to: '/activity', icon: Activity, label: 'nav.activity' },
]

/**
 * Everything belonging to this account, in the order the desktop rail uses.
 *
 * The group was "From YouTube" and held only the two read-only mirrors; the
 * profile and the YouTube connection have joined them, because that is one
 * subject and it was filed in two places — here under Preferences, and on the
 * desktop nowhere at all.
 *
 * Watch later and Playlists are still a copy of what the account says and still
 * cannot be edited here; that promise now sits on those two items rather than on
 * the heading, which no longer only means "read-only".
 *
 * On a phone this is the way in — the bottom bar is full at five, and what earns
 * a place there is what you move between while browsing. The avatar in the top
 * bar is the other door to the same room.
 */
const PHONE_ACCOUNT: MenuItem[] = [
  { to: '/profile', icon: UserRound, label: 'nav.profile' },
  { to: '/account', icon: KeyRound, label: 'youtubeAccount.title' },
  { to: '/watch-later', icon: Clock, label: 'nav.watchLater' },
  { to: '/playlists', icon: ListVideo, label: 'nav.playlists' },
]

const PHONE_PREFS: MenuItem[] = [
  { to: '/settings/feed', icon: LayoutGrid, label: 'settings.feedMix.title' },
  { to: '/settings/narration', icon: Headphones, label: 'phoneSettings.narration' },
  { to: '/settings/translation', icon: Languages, label: 'phoneSettings.translation' },
  { to: '/settings/advanced', icon: SlidersHorizontal, label: 'phoneSettings.advanced' },
]

/** A row in the phone settings menu. `label` is a key — see the note above. */
interface MenuItem {
  to: string
  icon: typeof Bookmark
  label: TranslationKey
}

function PhoneMenu() {
  const { t } = useTranslation()
  return (
    <nav className="mt-4 flex flex-col gap-6" aria-label={t('ui.settings')}>
      <MenuGroup label={t('phoneSettings.library')} items={PHONE_LIBRARY} />
      <MenuGroup label={t('phoneSettings.account')} items={PHONE_ACCOUNT} />
      <MenuGroup label={t('phoneSettings.preferences')} items={PHONE_PREFS} />
    </nav>
  )
}

function MenuGroup({
  label,
  items,
}: {
  /** Already translated: the group headings are written at the call site. */
  label: string
  items: MenuItem[]
}) {
  const { t } = useTranslation()

  return (
    <div>
      <h2 className="px-1 text-xs font-medium uppercase tracking-wide text-text-2">
        {label}
      </h2>
      <div className="mt-1">
        {items.map(({ to, icon: Icon, label }) => (
          <PageLink
            key={to}
            to={to}
            className="flex items-center gap-3 rounded-xl px-1 py-3.5 text-sm
                       transition-colors hover:bg-surface-hover"
          >
            <Icon size={18} className="shrink-0 text-text-2" />
            <span className="flex-1">{t(label)}</span>
            <ChevronRight size={18} className="shrink-0 text-text-2" />
          </PageLink>
        ))}
      </div>
    </div>
  )
}

export function NarrationSettings({ headless = false }: { headless?: boolean } = {}) {
  const { t } = useTranslation()
  const [prefs, setPrefs] = useState(loadNarrationAudioPrefs)

  // Where speech is synthesised. Server-side, unlike the levels below, because
  // it holds a credential and because it is a property of the installation
  // rather than of this browser.
  const { data: ttsConfig } = useTTSConfig()
  const saveTTS = useSaveTTSConfig()
  const testTTS = useTestTTS()
  const [ttsBaseUrl, setTTSBaseUrl] = useState('')
  const [ttsModel, setTTSModel] = useState('')
  const [ttsKey, setTTSKey] = useState('')
  const [showTTSKey, setShowTTSKey] = useState(false)

  useEffect(() => {
    if (!ttsConfig) return
    setTTSBaseUrl(ttsConfig.baseUrl)
    setTTSModel(ttsConfig.model)
  }, [ttsConfig])

  // Named once. Test and Save acting on two separately assembled objects is how
  // "it worked when I tested it" starts being true and useless.
  const ttsForm = {
    baseUrl: ttsBaseUrl,
    model: ttsModel,
    apiKey: ttsKey,
    voice: prefs.voice,
  }
  const ttsResult = testTTS.data

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
      title={t('ui.narration')}
      description={t('narration.tryIt')}
    >
      {/* The endpoint comes first, because until it is set there is no sound
          to balance and the sliders below are settings for nothing. */}
      <SettingRow label={t('narration.baseURL')} hint={t('narration.openaiFormat')}>
        <input
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none ring-1 ring-line focus:ring-2 focus:ring-ring"
          value={ttsBaseUrl}
          placeholder="https://api.openai.com/v1"
          aria-label={t('narration.baseURL')}
          onChange={(e) => setTTSBaseUrl(e.target.value)}
        />
      </SettingRow>

      <SettingRow
        label={t('translationSettings.apiKey')}
        hint={
          ttsConfig?.hasKey
            ? t('ui.keyStored', { hint: ttsConfig.keyHint })
            : t('translationSettings.noKeyStored')
        }
      >
        <input
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none ring-1 ring-line focus:ring-2 focus:ring-ring"
          type={showTTSKey ? 'text' : 'password'}
          value={ttsKey}
          placeholder={ttsConfig?.hasKey ? '••••••••' : 'sk-…'}
          aria-label={t('translationSettings.apiKey')}
          onChange={(e) => setTTSKey(e.target.value)}
        />
        <button
          type="button"
          aria-label={showTTSKey ? t('translationSettings.hideKey') : t('translationSettings.showKey')}
          onClick={() => setShowTTSKey((v) => !v)}
          className="grid h-11 w-11 shrink-0 place-items-center rounded-lg bg-surface-hover text-text-2 transition-colors duration-150 ease-out hover:text-text"
        >
          {showTTSKey ? <EyeOff size={16} /> : <Eye size={16} />}
        </button>
      </SettingRow>

      <SettingRow label={t('ui.model')} hint={t('narration.modelHint')}>
        <input
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none ring-1 ring-line focus:ring-2 focus:ring-ring"
          value={ttsModel}
          placeholder="gpt-4o-mini-tts"
          aria-label={t('ui.model')}
          onChange={(e) => setTTSModel(e.target.value)}
        />
      </SettingRow>

      {/* The same two buttons as the translation panel, in the same order and
          the same weights: Test is the quiet one, Save is the committing one.
          Two settings screens that do the same job should not ask to be read
          twice. */}
      <ActionBar>
        <button
          type="button"
          onClick={() => testTTS.mutate(ttsForm)}
          disabled={testTTS.isPending}
          className="h-11 rounded-lg bg-surface-hover px-5 text-sm font-medium transition-colors duration-150 ease-out hover:bg-white/15 disabled:opacity-50"
        >
          {testTTS.isPending ? t('ui.testing') : t('ui.test')}
        </button>
        <button
          type="button"
          onClick={() => saveTTS.mutate(ttsForm)}
          disabled={saveTTS.isPending}
          className="h-11 rounded-lg bg-invert-bg px-5 text-sm font-medium text-invert-text transition-opacity duration-150 ease-out hover:opacity-90 disabled:opacity-50"
        >
          {saveTTS.isPending ? t('ui.saving') : t('common.save')}
        </button>
      </ActionBar>

      {/* What the test produced, played rather than described. An endpoint can
          answer 200 with perfectly formed silence, and a number of milliseconds
          would report that as a success. */}
      {ttsResult?.error && (
        <p role="alert" className="text-xs text-brand">
          {ttsResult.error}
        </p>
      )}
      {ttsResult?.audio && (
        <div className="rounded-lg bg-surface-input p-3">
          <p className="text-xs text-text-2">{ttsResult.sample}</p>
          {/* Native controls: this is one clip on a settings page, and a custom
              player here would be a second thing to keep working for no gain. */}
          <audio src={ttsResult.audio} controls className="mt-2 w-full" />
          <p className="mt-1 text-xs text-text-2">
            {t('more.milliseconds', { ms: ttsResult.ms ?? 0 })}
          </p>
        </div>
      )}

      <div className="mt-4 rounded-lg bg-surface-input p-3">
        <h3 className="mb-3 text-xs font-medium uppercase tracking-wide text-text-2">{t('ui.audio')}</h3>
        <div className="flex flex-col gap-4">
          {/* Typed, not chosen.
              
              It was a menu filled from the synthesiser's own voice list. OpenAI
              publishes no such endpoint — its voices are a fixed set in the
              documentation — and every service that copies the API brings its
              own names, so a menu would be right for one provider and wrong the
              day it added a voice, in the worst way: the voice exists and this
              app refuses it.
              
              Still per device, which the levels below are too: two people in
              one house should be able to disagree about a voice. */}
          <SettingRow label={t('ui.voice')} hint={t('narration.voiceHint')}>
            <input
              className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none ring-1 ring-line focus:ring-2 focus:ring-ring"
              value={prefs.voice}
              placeholder={DEFAULT_VOICE}
              aria-label={t('ui.voice')}
              onChange={(e) => update({ voice: e.target.value })}
            />
          </SettingRow>

          <SliderRow
            label={t('narration.voiceVolume')}
            value={prefs.voiceLevel}
            max={MAX_VOICE_LEVEL}
            format={percent}
            onChange={(voiceLevel) => update({ voiceLevel })}
            hint={t('narration.voiceVolumeHint')}
          />

          <SliderRow
            label={t('narration.videoVolumeWhileSpeaking')}
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
  const { t } = useTranslation()
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
      title={t('ui.translation')}
      description={t('translationSettings.description')}
    >
      <SettingRow label={t('translationSettings.baseURL')}>
        <input
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none ring-1 ring-line focus:ring-2 focus:ring-ring"
          value={baseUrl}
          placeholder="http://host:port"
          aria-label={t('translationSettings.baseURL')}
          onChange={(e) => setBaseUrl(e.target.value)}
        />
      </SettingRow>

      <SettingRow
        label={t('translationSettings.apiKey')}
        hint={
          config?.hasKey
            ? t('ui.keyStored', { hint: config.keyHint })
            : t('translationSettings.noKeyStored')
        }
      >
        <input
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none ring-1 ring-line focus:ring-2 focus:ring-ring"
          type={showKey ? 'text' : 'password'}
          value={apiKey}
          placeholder={config?.hasKey ? '••••••••' : 'sk-…'}
          aria-label={t('translationSettings.apiKey')}
          onChange={(e) => setApiKey(e.target.value)}
        />
        <button
          type="button"
          aria-label={showKey ? t('translationSettings.hideKey') : t('translationSettings.showKey')}
          onClick={() => setShowKey((v) => !v)}
          className="grid h-11 w-11 shrink-0 place-items-center rounded-lg bg-surface-hover text-text-2 transition-colors duration-150 ease-out hover:text-text"
        >
          {showKey ? <EyeOff size={16} /> : <Eye size={16} />}
        </button>
      </SettingRow>

      <SettingRow label={t('ui.model')}>
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
          {test.isPending ? t('ui.testing') : t('ui.test')}
        </button>
        <button
          type="button"
          onClick={() => save.mutate(form)}
          disabled={save.isPending}
          className="h-11 rounded-lg bg-invert-bg px-5 text-sm font-medium text-invert-text transition-opacity duration-150 ease-out hover:opacity-90 disabled:opacity-50"
        >
          {save.isPending ? t('ui.saving') : t('common.save')}
        </button>
      </ActionBar>

      {models.isError && (
        <p className="text-xs text-brand">{t('translationSettings.couldNotLoadModels')}</p>
      )}
      {models.data && (
        <p className="text-xs text-text-2">
          {t('more.modelsAvailable', { count: models.data.length })}
        </p>
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
              <p className="mt-1 text-xs text-text-2">{t('more.milliseconds', { ms: result.ms ?? 0 })}</p>
            </>
          )}
        </div>
      )}
      {test.isError && (
        <p className="text-xs text-brand">{t('translationSettings.testFailed')}</p>
      )}
      {save.isSuccess && !save.isPending && (
        <p className="text-xs text-text-2">{t('translationSettings.savedNextBatch')}</p>
      )}
    </SettingsSection>
  )
}
