import { AlertTriangle, RotateCcw, SlidersHorizontal } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useRanking, useSaveRanking } from '@/features/settings/application/queries'
import {
  isUnset,
  type RankingField,
  RANKING_FIELDS,
  type RankingSettings,
  sameSettings,
  setField,
  valueOf,
} from '@/features/settings/domain/ranking'
import { ActionBar } from '@/features/settings/ui/ActionBar'
import { SettingsSection } from '@/features/settings/ui/SettingsSection'
import { SliderRow } from '@/features/settings/ui/SliderRow'
import { useTranslation } from 'react-i18next'

/**
 * The ranking constants, for a household that wants to move them.
 *
 * Its own section rather than a disclosure inside Home feed, because these are a
 * different question. That page asks what the home page is made of; this one
 * asks how the ranking behaves — and burying controls that can visibly break the
 * ordering underneath three benign sliders is how they get changed by accident.
 *
 * Not every weight in the ranker, deliberately. There are two dozen more, and
 * putting them here would trade the one advantage this heuristic has over a
 * learned model — that every number in it can be explained — for a control panel
 * nobody can account for six months later. These seven each answer a question
 * somebody actually has.
 *
 * Everything written here lands in a plain JSON file on the gateway, so it can
 * equally be edited by hand. Values outside their range are clamped by recsys
 * rather than refused: this decides the order of a grid, and a hand-edited file
 * with a typo in it must still produce a feed.
 */
export function AdvancedSettings({ headless = false }: { headless?: boolean } = {}) {
  const { t } = useTranslation()
  const { data: stored, isError, isPending, refetch } = useRanking()
  const save = useSaveRanking()
  const [draft, setDraft] = useState<RankingSettings | null>(null)

  // The server's copy is the starting point, and only until the first drag.
  useEffect(() => {
    if (stored && !draft) setDraft({ ...stored })
  }, [stored, draft])

  if (isError) {
    return (
      <SettingsSection
        headless={headless}
        icon={<SlidersHorizontal size={18} />}
        title={t('ui.advanced')}
        description={t('settings.advanced.couldNotRead')}
      >
        <button
          type="button"
          onClick={() => void refetch()}
          className="h-11 w-fit rounded-lg bg-surface-hover px-4 text-sm font-medium transition-opacity duration-150 ease-out hover:opacity-90"
        >
          Try again
        </button>
      </SettingsSection>
    )
  }

  if (isPending || !stored || !draft) {
    return (
      <SettingsSection
        headless={headless}
        icon={<SlidersHorizontal size={18} />}
        title={t('ui.advanced')}
        description={t('common.loading')}
      >
        <div className="h-24 animate-pulse rounded-lg bg-surface-input" />
      </SettingsSection>
    )
  }

  const dirty = !sameSettings(draft, stored)

  return (
    <SettingsSection
      headless={headless}
      icon={<SlidersHorizontal size={18} />}
      title={t('ui.advanced')}
      description={t('settings.advanced.intro')}
    >
      {RANKING_FIELDS.map((field) => (
        <RankingSlider
          key={field.key}
          field={field}
          settings={draft}
          onChange={(value) => setDraft((d) => (d ? setField(d, field.key, value) : d))}
        />
      ))}

      <ActionBar>
        <button
          type="button"
          disabled={!dirty || save.isPending}
          onClick={() => save.mutate(draft)}
          className="h-11 rounded-lg bg-invert-bg px-5 text-sm font-medium text-invert-text transition-opacity duration-150 ease-out hover:opacity-90 disabled:opacity-50"
        >
          {save.isPending ? t('ui.saving') : t('common.save')}
        </button>
        {/* Clears every override rather than writing today's numbers in. A
            setting left unset follows the ranker; one pinned to the current
            default does not, and the two are indistinguishable on screen. */}
        <button
          type="button"
          disabled={isUnset(draft) || save.isPending}
          onClick={() => setDraft({})}
          className="flex h-11 items-center gap-1.5 rounded-lg px-3 text-sm text-text-2 transition-colors duration-150 ease-out hover:bg-surface-hover disabled:opacity-50"
        >
          <RotateCcw size={14} />
          Use built-in values
        </button>
        {!dirty && save.isSuccess && (
          <span className="text-sm text-text-2">{t('settings.feedMix.savedRebuilt')}</span>
        )}
        {save.isError && (
          <span className="text-sm text-brand">{t('settings.feedMix.couldNotSave')}</span>
        )}
      </ActionBar>
    </SettingsSection>
  )
}

function RankingSlider({
  field,
  settings,
  onChange,
}: {
  field: RankingField
  settings: RankingSettings
  onChange: (value: number | undefined) => void
}) {
  const { t } = useTranslation()
  const value = valueOf(settings, field)
  const overridden = settings[field.key] !== undefined

  return (
    <div>
      <SliderRow
        label={t(field.label)}
        value={value}
        min={field.min}
        max={field.max}
        step={field.step}
        onChange={onChange}
        // The domain returns a key and a number; the words live in the
        // dictionary, because that file cannot call a hook and must not hold
        // English.
        format={(v) => {
          const [key, value] = field.format(v)
          return t(key, { value })
        }}
        hint={t(field.hint)}
        trailing={
          field.risky ? (
            <span
              title={t('settings.advanced.breaks')}
              className="flex items-center gap-1 text-xs text-text-2"
            >
              <AlertTriangle size={12} />
              careful
            </span>
          ) : undefined
        }
      />
      <div className="mt-1 flex items-baseline gap-3 text-xs text-text-2">
        <span>
          {t('settings.advanced.builtIn', {
            value: (() => {
              const [key, value] = field.format(field.fallback)
              return t(key, { value })
            })(),
          })}
        </span>
        {overridden && (
          <button
            type="button"
            onClick={() => onChange(undefined)}
            className="underline underline-offset-2 transition-opacity hover:opacity-80"
          >
            use built-in
          </button>
        )}
      </div>
    </div>
  )
}
