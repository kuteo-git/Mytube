import { AdvancedSettings } from '@/features/settings/ui/AdvancedSettings'
import { ProfileSettings } from '@/features/identity/ui/ProfileSettings'
import { YouTubeAccountSettings } from '@/features/settings/ui/YouTubeAccountSettings'
import { FeedMixSettings } from '@/features/settings/ui/FeedMixSettings'
import { usePlayer } from '@/features/watch/application/player-context'
import { NarrationSettings, TranscriptSettings, TranslationSettings } from './SettingsPage'

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

export function TranscriptSettingsPage() {
  return (
    <div className="px-4 pb-16">
      <TranscriptSettings headless />
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

/**
 * The two account screens, which are reached on a desktop as well as a phone.
 *
 * `headless` follows the device, and that is not a style choice: the back bar
 * that names a bare screen is drawn only when the phone chrome is hidden
 * (`chromeHidden` is mobile-only), so a desktop rendering these headless gets a
 * panel with no title on it at all. The four /settings/* siblings can stay
 * unconditionally headless — the desktop reaches those through the Settings
 * page, which shows them under its own headings. These two are on the rail.
 */
export function ProfileSettingsPage() {
  const { isMobile } = usePlayer()
  return (
    <div className="mx-auto max-w-3xl px-4 pb-16">
      <ProfileSettings headless={isMobile} />
    </div>
  )
}

export function YouTubeAccountPage() {
  const { isMobile } = usePlayer()
  return (
    <div className="mx-auto max-w-3xl px-4 pb-16">
      <YouTubeAccountSettings headless={isMobile} />
    </div>
  )
}
