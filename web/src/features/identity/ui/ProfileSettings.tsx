import { UserRound } from 'lucide-react'

import { SettingsSection } from '@/features/settings/ui/SettingsSection'
import { ProfilePicker } from './ProfilePicker'
import { useTranslation } from 'react-i18next'

/**
 * Switching profile, from Settings.
 *
 * The same picker the gate shows, because there is only one way to answer this
 * question and two screens asking it would drift. There is no "sign out": there
 * was never a session, and a button that ends nothing would be the dead control
 * §5 forbids.
 *
 * `manage` is the difference between the two: here each profile can be deleted,
 * at the gate none can. Somebody arriving at the gate is trying to start
 * watching, and a row of delete buttons in front of them is both noise and a
 * hazard.
 */
export function ProfileSettings({ headless = false }: { headless?: boolean }) {
  const { t } = useTranslation()
  return (
    <SettingsSection
      icon={<UserRound size={18} />}
      title="Profile"
      description={t('profiles.whoIsThis')}
      headless={headless}
    >
      <ProfilePicker manage />
    </SettingsSection>
  )
}
