import { useEffect, useState } from 'react'
import { FolderOpen } from 'lucide-react'
import clsx from 'clsx'

import { apiJSON } from '@/shared/api/http'
import { formatBytes } from '@/shared/lib/format'
import { SettingsSection } from './SettingsSection'
import { ActionBar } from './ActionBar'
import { useTranslation } from 'react-i18next'

/**
 * Where the library lives, and whether it is kept at all.
 *
 * At the top of the Storage page, above the figures, because those figures
 * describe *that folder* under *that mode* — reading them first is reading them
 * without their subject.
 *
 * The two settings share this card and nothing else. The path is held by three
 * services from start-up and takes a restart; keeping is one condition the
 * gateway reads on every request and takes effect at once. Saying so on screen
 * is the difference between "it did nothing" and "it will, in a moment".
 */

interface StorageSettingsState {
  mediaRoot: string
  source: 'file' | 'environment'
  cacheDisabled: boolean
  restartRequired?: boolean
}

interface RootCheck {
  ok: boolean
  problem?: string
  freeBytes?: number
  videoCount?: number
}

export function StorageSettings({ headless = false }: { headless?: boolean }) {
  const { t } = useTranslation()
  const [state, setState] = useState<StorageSettingsState | null>(null)
  const [path, setPath] = useState('')
  const [check, setCheck] = useState<RootCheck | null>(null)
  const [checking, setChecking] = useState(false)
  const [saved, setSaved] = useState('')
  const [error, setError] = useState('')
  const [confirming, setConfirming] = useState<{ videos: number; oldRoot: string } | null>(null)

  useEffect(() => {
    apiJSON<StorageSettingsState>('/api/settings/storage')
      .then((s) => {
        setState(s)
        setPath(s.mediaRoot)
      })
      .catch(() => setError(t('settings.storage.couldNotRead')))
  }, [])

  // Checked on leaving the field as well as on the button. Validating only when
  // something is submitted is the pattern to avoid anywhere; here "submit"
  // means a restart, so finding out then is finding out far too late.
  const verify = async (candidate: string) => {
    if (!candidate.trim() || candidate === state?.mediaRoot) return
    setChecking(true)
    setError('')
    try {
      setCheck(await apiJSON<RootCheck>(`/api/settings/storage/verify?path=${encodeURIComponent(candidate)}`))
    } catch {
      setCheck({ ok: false, problem: t('settings.storage.couldNotReach') })
    } finally {
      setChecking(false)
    }
  }

  const save = async (confirmed = false) => {
    setError('')
    setSaved('')
    try {
      const next = await apiJSON<StorageSettingsState>('/api/settings/storage', {
        method: 'POST',
        body: JSON.stringify({ mediaRoot: path, confirmed }),
      })
      setState(next)
      setConfirming(null)
      setSaved(
        next.restartRequired
          ? t('settings.storage.savedRestart')
          : t('ui.savedFull'),
      )
    } catch (e) {
      // The gateway answers 409 with the count when the old folder still holds
      // videos. That is a question, not a failure.
      const body = e instanceof Error ? e.message : ''
      const match = /videosAtOldRoot"?:\s*(\d+)/.exec(body)
      if (match) {
        setConfirming({ videos: Number(match[1]), oldRoot: state?.mediaRoot ?? '' })
        return
      }
      setError(body || t('settings.storage.couldNotSaveFolder'))
    }
  }

  const setCache = async (disabled: boolean) => {
    setError('')
    try {
      setState(
        await apiJSON<StorageSettingsState>('/api/settings/storage', {
          method: 'POST',
          body: JSON.stringify({ cacheDisabled: disabled }),
        }),
      )
    } catch {
      setError(t('settings.storage.couldNotChange'))
    }
  }

  return (
    <SettingsSection
      icon={<FolderOpen size={18} />}
      // Not "Storage": the page heading an inch above already says that word,
      // which is the fault SettingsSection's own note warns about.
      title={t('settings.storage.title')}
      description={t('settings.storage.description')}
      headless={headless}
    >
      <label className="mt-4 block text-sm text-text-2" htmlFor="media-root">
        Folder
      </label>
      <input
        id="media-root"
        value={path}
        onChange={(e) => {
          setPath(e.target.value)
          setCheck(null)
          setSaved('')
        }}
        onBlur={(e) => void verify(e.target.value)}
        spellCheck={false}
        placeholder="/Volumes/Data2/Youtube"
        className="mt-1 w-full rounded-lg bg-surface-input p-3 font-mono text-sm outline-none ring-1 ring-border focus:ring-2 focus:ring-brand"
      />

      {/* Where the current value came from. Somebody who cannot tell a saved
          setting from an inherited default cannot tell a working setting from
          one being ignored — and being ignored is the likely failure here,
          because dev.sh exports MEDIA_ROOT on every run. */}
      {state && (
        <p className="pt-1 text-xs text-text-2">
          {state.source === 'file'
            ? t('settings.storage.savedHere')
            : t('storageMode.fromEnvironment')}
        </p>
      )}

      {/* The answer sits next to the field, and carries what to do about it. */}
      {check && (
        <p
          role={check.ok ? 'status' : 'alert'}
          className={clsx('pt-2 text-sm', check.ok ? 'text-text-2' : 'text-brand')}
        >
          {check.ok
            ? `Writable. ${formatBytes(check.freeBytes ?? 0)} free, ${check.videoCount ?? 0} videos already there.`
            : check.problem}
        </p>
      )}

      <ActionBar>
        <button
          type="button"
          onClick={() => void verify(path)}
          disabled={checking || !path.trim()}
          className="mt-3 rounded-full bg-surface-hover px-4 py-2 text-sm transition-colors duration-150 ease-out disabled:opacity-60"
        >
          {checking ? t('ui.checking') : t('ui.check')}
        </button>
        <button
          type="button"
          onClick={() => void save()}
          disabled={checking || !check?.ok}
          className="mt-3 rounded-full bg-brand px-4 py-2 text-sm font-medium text-white transition-colors duration-150 ease-out disabled:opacity-60"
        >
          Save
        </button>
        {saved && (
          <span role="status" className="mt-3 text-sm text-text-2">
            {saved}
          </span>
        )}
        {error && (
          <span role="alert" className="mt-3 text-sm text-brand">
            {error}
          </span>
        )}
      </ActionBar>

      {confirming && (
        <div role="alert" className="mt-4 rounded-lg bg-surface-hover p-3 text-sm">
          <p>
            <span className="font-medium">{confirming.videos.toLocaleString()}</span> videos are at{' '}
            <span className="font-mono text-xs">{confirming.oldRoot}</span>. Changing the folder
            does not move them — they would have to be downloaded again.
          </p>
          <p className="pt-2 text-text-2">
            To keep them, move them yourself first, then change this.
          </p>
          <div className="flex gap-2 pt-3">
            <button
              type="button"
              onClick={() => setConfirming(null)}
              className="rounded-full px-3 py-1.5 text-sm transition-colors duration-150 ease-out hover:bg-surface"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => void save(true)}
              className="rounded-full bg-brand px-3 py-1.5 text-sm font-medium text-white transition-colors duration-150 ease-out"
            >
              Change anyway
            </button>
          </div>
        </div>
      )}

      {/* The same switch shape as the player's settings: the control is its own
          feedback, so the row has no hover fill saying it a second time. */}
      <button
        type="button"
        role="switch"
        aria-checked={state?.cacheDisabled ?? false}
        onClick={() => void setCache(!state?.cacheDisabled)}
        disabled={!state}
        className="mt-6 flex w-full items-center justify-between gap-4 text-left"
      >
        <span>
          <span className="block text-sm">{t('storageMode.streamOnly')}</span>
          <span className="block pt-0.5 text-xs text-text-2">
            Videos play from YouTube and are not downloaded. Subtitles still
            arrive, files already here still play, and Retry still works.
          </span>
        </span>
        <span
          className={clsx(
            'relative h-6 w-11 shrink-0 rounded-full transition-colors duration-150 ease-out',
            state?.cacheDisabled ? 'bg-brand' : 'bg-surface-hover',
          )}
        >
          <span
            className={clsx(
              'absolute top-1 h-4 w-4 rounded-full bg-white transition-transform duration-150 ease-out',
              state?.cacheDisabled ? 'translate-x-6' : 'translate-x-1',
            )}
          />
        </span>
      </button>
    </SettingsSection>
  )
}
