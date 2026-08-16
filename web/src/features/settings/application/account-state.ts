import { useQuery } from '@tanstack/react-query'

import { accountRepository } from '../infrastructure/accountRepository'

/**
 * The viewer's own YouTube session state.
 *
 * Theirs only — the gateway answers per profile on purpose, and one person's
 * session is not another's business (`accounts.go`). Shared here because more
 * than one screen has to explain itself differently once a session has ended:
 * the banner says it, and so must anything that is waiting on the import.
 *
 * Checked rarely. This changes about as often as a password does.
 */
export function useAccountState() {
  const { data } = useQuery({
    queryKey: ['youtube-account'],
    queryFn: () => accountRepository.get(),
    staleTime: 10 * 60 * 1000,
    refetchInterval: 10 * 60 * 1000,
  })
  return {
    state: data?.state ?? 'NEVER_SET',
    signedOut: data?.state === 'EXPIRED',
    connected: data?.state === 'OK',
  }
}
