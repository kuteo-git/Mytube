import { Eye, EyeOff, Globe, TriangleAlert } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ActionBar } from '@/features/settings/ui/ActionBar'
import { SettingRow, SettingsSection } from '@/features/settings/ui/SettingsSection'
import { ToggleRow } from '@/features/settings/ui/ToggleRow'
import {
  useProxyConfig,
  useSaveProxyConfig,
  useTestProxy,
} from '@/features/settings/application/queries'
import type { ProxyConfig } from '@/features/settings/infrastructure/settingsRepository'

/**
 * The outbound proxy, and which traffic goes through it.
 *
 * ## Why this screen exists
 *
 * YouTube refuses by **public address**. Measured 2026-08-28, four videos, one
 * minute, one machine: asking for captions directly was blocked 4 of 4, and
 * asking through a rotating residential proxy succeeded 4 of 4. Nothing about
 * the shape of a request changes that, so the address is the only lever.
 *
 * It replaces the Transcript screen, which asked which *other machine* to ask —
 * a premise measured to be false, since another machine in the same house is the
 * same address.
 *
 * ## Why one URL field
 *
 * `scheme://user:pass@host:port` is what every provider hands out and what both
 * consumers take. Four separate boxes would mean everybody splits the string by
 * hand and the server joins it again: two more places to be wrong, no gain.
 *
 * ## Why the traffic is chosen one kind at a time
 *
 * These differ by three orders of magnitude — a caption file is tens of
 * kilobytes, a 1080p video is hundreds of megabytes — and a residential proxy is
 * sold by the gigabyte. One switch covering both is somebody turning on captions
 * and losing a month of bandwidth by morning with nothing having said so.
 */
export function ProxySettings({ headless = false }: { headless?: boolean } = {}) {
  const { t } = useTranslation()
  const { data: config } = useProxyConfig()
  const save = useSaveProxyConfig()
  const test = useTestProxy()

  const [form, setForm] = useState<ProxyConfig>(emptyProxy)
  const [showURL, setShowURL] = useState(false)
  // The one switch that costs money, so the one that asks first.
  const [confirmingMedia, setConfirmingMedia] = useState(false)

  useEffect(() => {
    if (config) setForm(config)
  }, [config])

  const set = (patch: Partial<ProxyConfig>) => setForm((f) => ({ ...f, ...patch }))
  const hasURL = form.url.trim() !== ''
  const result = test.data

  return (
    <SettingsSection
      headless={headless}
      icon={<Globe size={18} />}
      title={t('proxySettings.title')}
      description={t('proxySettings.description')}
    >
      <SettingRow label={t('proxySettings.url')} hint={t('proxySettings.urlHint')}>
        <input
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 font-mono text-sm outline-none ring-1 ring-line focus:ring-2 focus:ring-ring"
          // Hidden by default because it carries a password and this screen is
          // opened on a television in a living room as readily as on a laptop.
          // The stored value arrives already masked, so revealing it shows the
          // bullets rather than the secret — only what is typed here is ever
          // visible in full.
          type={showURL ? 'text' : 'password'}
          value={form.url}
          placeholder="http://user:pass@p.example.io:80"
          aria-label={t('proxySettings.url')}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => set({ url: e.target.value })}
        />
        <button
          type="button"
          aria-label={showURL ? t('translationSettings.hideKey') : t('translationSettings.showKey')}
          onClick={() => setShowURL((v) => !v)}
          className="grid h-11 w-11 shrink-0 place-items-center rounded-lg bg-surface-hover text-text-2 transition-colors duration-150 ease-out hover:text-text"
        >
          {showURL ? <EyeOff size={16} /> : <Eye size={16} />}
        </button>
      </SettingRow>

      <div className="mt-2 flex flex-col gap-1">
        <ToggleRow
          label={t('proxySettings.enabled')}
          hint={t('proxySettings.enabledHint')}
          on={form.enabled}
          // Nothing to route through: a master switch that turns on with no
          // address is §5's dead button with a delay on it — everything looks
          // configured and nothing changes.
          disabled={!hasURL}
          onChange={(on) => set({ enabled: on })}
        />

        {/* The four kinds, indented under the switch that governs them. They
            keep their values while the master is off rather than reading false:
            turning the proxy off for an evening should not lose which traffic
            was going through it. */}
        <ToggleRow
          indented
          label={t('proxySettings.forCaptions')}
          hint={t('proxySettings.forCaptionsHint')}
          on={form.forCaptions}
          disabled={!form.enabled}
          onChange={(on) => set({ forCaptions: on })}
        />
        <ToggleRow
          indented
          label={t('proxySettings.forListings')}
          hint={t('proxySettings.forListingsHint')}
          on={form.forListings}
          disabled={!form.enabled}
          onChange={(on) => set({ forListings: on })}
        />
        <ToggleRow
          indented
          label={t('proxySettings.forComments')}
          hint={t('proxySettings.forCommentsHint')}
          on={form.forComments}
          disabled={!form.enabled}
          onChange={(on) => set({ forComments: on })}
        />
        <ToggleRow
          indented
          label={t('proxySettings.forMedia')}
          hint={t('proxySettings.forMediaHint')}
          on={form.forMedia}
          disabled={!form.enabled}
          // Turning it *on* asks; turning it off never does. A confirmation on
          // the way out of an expensive state is a toll on doing the safe thing.
          onChange={(on) => (on ? setConfirmingMedia(true) : set({ forMedia: false }))}
        >
          {confirmingMedia && (
            <div className="mt-2 rounded-lg bg-surface-input p-3 ring-1 ring-line">
              <p className="flex items-start gap-2 text-sm">
                <TriangleAlert size={16} className="mt-0.5 shrink-0 text-brand" />
                <span>{t('proxySettings.mediaWarning')}</span>
              </p>
              <div className="mt-3 flex gap-2">
                <button
                  type="button"
                  onClick={() => {
                    set({ forMedia: true })
                    setConfirmingMedia(false)
                  }}
                  className="rounded-full bg-brand px-3 py-1.5 text-sm font-medium text-white transition-colors duration-150 ease-out"
                >
                  {t('proxySettings.mediaConfirm')}
                </button>
                <button
                  type="button"
                  onClick={() => setConfirmingMedia(false)}
                  className="rounded-full bg-surface-hover px-3 py-1.5 text-sm font-medium transition-colors duration-150 ease-out"
                >
                  {t('common.cancel')}
                </button>
              </div>
            </div>
          )}
        </ToggleRow>
      </div>

      <ActionBar>
        <button
          type="button"
          onClick={() => test.mutate(form)}
          disabled={test.isPending || !hasURL}
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

      {result && <ProxyTestReport result={result} />}
      {test.isError && <p className="text-xs text-brand">{t('proxySettings.testFailed')}</p>}
      {save.isSuccess && !save.isPending && (
        <p className="text-xs text-text-2">{t('proxySettings.saved')}</p>
      )}
    </SettingsSection>
  )
}

/**
 * What the test found, as three answers rather than one verdict.
 *
 * The addresses side by side are the point. Equal means the proxy carried the
 * request and left the address alone — the failure most easily mistaken for "the
 * proxy is broken" when it is doing exactly what it was told, and invisible
 * without both numbers.
 */
function ProxyTestReport({
  result,
}: {
  result: NonNullable<ReturnType<typeof useTestProxy>['data']>
}) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col gap-2 rounded-lg bg-surface-input p-3 text-sm">
      <div className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-1">
        <span className="text-xs text-text-2">{t('proxySettings.directAddress')}</span>
        <span className="font-mono text-xs">{result.directIp || '—'}</span>
        <span className="text-xs text-text-2">{t('proxySettings.proxyAddress')}</span>
        <span className="font-mono text-xs">{result.proxyIp || '—'}</span>
      </div>

      {result.code && <p className="text-brand">{t(`proxyError.${result.code}` as never)}</p>}

      {result.captionsOk ? (
        <p className="text-xs text-text-2">
          {t('proxySettings.gotCues', {
            language: result.captionsLang ?? '',
            count: result.cues ?? 0,
          })}
        </p>
      ) : (
        result.captionsCode && (
          <p className="text-brand">{t(`proxyError.${result.captionsCode}` as never)}</p>
        )
      )}

      <p className="text-xs text-text-2">{t('more.milliseconds', { ms: result.tookMs })}</p>
    </div>
  )
}

const emptyProxy: ProxyConfig = {
  url: '',
  enabled: false,
  forCaptions: false,
  forListings: false,
  forMedia: false,
  forComments: false,
}
