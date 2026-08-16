import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'

import { accountRepository } from '../infrastructure/accountRepository'

/**
 * Says when a YouTube session has ended.
 *
 * Cookies expire; this is when, not if. Once a session dies the account stops
 * being used entirely — a dead cookie replayed hourly is how a blocked address
 * becomes a banned account — so without something on screen the household's
 * subscriptions would quietly stop updating and nothing would ever say why.
 *
 * Only for a session that has actually ended. A banner that is up while things
 * work is a banner nobody reads on the day they need to.
 */
export function CookieExpiryBanner() {
  const { data: account } = useQuery({
    queryKey: ['youtube-account'],
    queryFn: () => accountRepository.get(),
    // Checked rarely: this changes about as often as a password does, and the
    // banner is the least urgent thing on any page it appears on.
    staleTime: 10 * 60 * 1000,
    refetchInterval: 10 * 60 * 1000,
  })

  if (account?.state !== 'EXPIRED') return null

  return (
    <div
      role="status"
      className="flex items-center justify-between gap-3 bg-brand/15 px-4 py-2 text-sm"
    >
      <span className="min-w-0 truncate">
        YouTube signed you out — subscriptions are no longer updating.
      </span>
      <Link
        to="/settings/youtube-account"
        className="shrink-0 rounded-lg bg-invert-bg px-3 py-1.5 text-xs font-medium text-invert-text"
      >
        Reconnect
      </Link>
    </div>
  )
}
