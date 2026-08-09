import { AdvancedSettings } from '@/features/settings/ui/AdvancedSettings'
import { FeedMixSettings } from '@/features/settings/ui/FeedMixSettings'
import { NarrationSettings, TranslationSettings } from './SettingsPage'

/**
 * One panel of Settings, on a screen of its own.
 *
 * A phone's Settings is a list of rows rather than three panels stacked down
 * one page: on a 390px column that is a page you scroll through hunting for the
 * one control you came for, and it is never the one on top. Each of these is
 * about a single thing, and the shell gives it a back bar and a title — see
 * bare-screens.ts.
 *
 * The panels themselves are unchanged and shared with the desktop page, which
 * still shows all three together. There is one implementation of each control,
 * and only the arrangement differs.
 */
export function FeedSettingsPage() {
  return (
    <div className="px-4 pb-16">
      <FeedMixSettings headless />
    </div>
  )
}

export function NarrationSettingsPage() {
  return (
    <div className="px-4 pb-16">
      <NarrationSettings headless />
    </div>
  )
}

export function TranslationSettingsPage() {
  return (
    <div className="px-4 pb-16">
      <TranslationSettings headless />
    </div>
  )
}

export function AdvancedSettingsPage() {
  return (
    <div className="px-4 pb-16">
      <AdvancedSettings headless />
    </div>
  )
}
