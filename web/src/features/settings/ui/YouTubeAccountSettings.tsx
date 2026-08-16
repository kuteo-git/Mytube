import { useState } from 'react'
import { KeyRound } from 'lucide-react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import { SettingsSection } from './SettingsSection'
import { accountRepository } from '../infrastructure/accountRepository'

/**
 * Connecting a household member's own YouTube account.
 *
 * The screen is mostly instructions, and they are the part that matters. A
 * person following them is about to hand a browser extension their entire
 * Google session, so the extension is named exactly and the near-identical one
 * that was pulled from the Chrome Web Store as malware is named too. Getting
 * that wrong is the worst thing this page could cause.
 *
 * What it never does is show the session back. There is no route that returns
 * it and no state here that holds it after the paste — the textarea is cleared
 * the moment the server accepts it.
 */
export function YouTubeAccountSettings({ headless = false }: { headless?: boolean }) {
  const queryClient = useQueryClient()
  const { data: account } = useQuery({
    queryKey: ['youtube-account'],
    queryFn: () => accountRepository.get(),
  })

  const [cookies, setCookies] = useState('')
  const [error, setError] = useState('')

  const save = useMutation({
    mutationFn: () => accountRepository.save(cookies, ''),
    onSuccess: () => {
      // Cleared immediately. Leaving a live session sitting in a React state
      // for the rest of the session is the one thing this screen can do wrong
      // after the paste has already worked.
      setCookies('')
      setError('')
      void queryClient.invalidateQueries({ queryKey: ['youtube-account'] })
    },
    onError: (e: Error) => setError(e.message),
  })

  const remove = useMutation({
    mutationFn: () => accountRepository.remove(),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['youtube-account'] }),
  })

  const scan = useMutation({
    mutationFn: () => accountRepository.scanNow(),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['youtube-account'] }),
  })

  const state = account?.state ?? 'NEVER_SET'

  return (
    <SettingsSection
      icon={<KeyRound size={18} />}
      title="YouTube account"
      // Names what the import actually does. It said "playlists", which nothing
      // here has ever brought in — there is no playlist anywhere in this system.
      // §5's rule against a button that does nothing holds for a sentence that
      // promises something too.
      description="Brings your own subscriptions and liked videos into the library. Your account, on this machine only."
      headless={headless}
    >
      <div className="pt-2">
        <p className="text-sm">
          <span className="text-text-2">Status: </span>
          {state === 'OK' && <span>Connected</span>}
          {state === 'EXPIRED' && (
            <span className="text-brand">Signed out — paste your cookies again</span>
          )}
          {state === 'NEVER_SET' && <span className="text-text-2">Not connected</span>}
        </p>
        {account?.lastResult && (
          <p className="pt-1 text-xs text-text-2">Last scan: {account.lastResult}</p>
        )}
      </div>

      <ol className="flex list-decimal flex-col gap-1 pl-5 pt-4 text-xs text-text-2">
        <li>
          Install{' '}
          <a
            className="text-text underline"
            href="https://chromewebstore.google.com/detail/get-cookiestxt-locally/cclelndahbckbenkjhflpdbgdldlbecc"
            target="_blank"
            rel="noreferrer"
          >
            Get cookies.txt LOCALLY
          </a>{' '}
          for Chrome — the one yt-dlp's own FAQ recommends.
        </li>
        <li>Open youtube.com signed in, click the extension, choose Netscape format.</li>
        <li>Paste the whole file below.</li>
      </ol>

      {/*
        Named because the mistake is expensive and easy. An extension called
        "Get cookies.txt", without LOCALLY, was reported as malware and removed
        from the Chrome Web Store — and anyone reading this page is about to
        hand one of them a live Google session.
      */}
      <p className="pt-2 text-xs text-brand">
        Do not install "Get cookies.txt" without LOCALLY — that one was removed
        from the store as malware.
      </p>

      <textarea
        value={cookies}
        onChange={(e) => setCookies(e.target.value)}
        placeholder="# Netscape HTTP Cookie File…"
        aria-label="Cookies file"
        rows={5}
        spellCheck={false}
        className="mt-3 w-full rounded-lg bg-surface-input p-3 font-mono text-xs outline-none ring-1 ring-border focus:ring-2 focus:ring-brand"
      />
      {error && (
        <p className="pt-1 text-xs text-brand" role="alert">
          {error}
        </p>
      )}

      <div className="flex flex-wrap gap-2 pt-3">
        <button
          type="button"
          disabled={!cookies.trim() || save.isPending}
          onClick={() => save.mutate()}
          className="min-h-11 rounded-lg bg-invert-bg px-4 text-sm font-medium text-invert-text disabled:opacity-50"
        >
          {save.isPending ? 'Saving…' : 'Connect'}
        </button>
        {state !== 'NEVER_SET' && (
          <>
            <button
              type="button"
              disabled={scan.isPending}
              onClick={() => scan.mutate()}
              className="min-h-11 rounded-lg bg-surface px-4 text-sm disabled:opacity-50"
            >
              {scan.isPending ? 'Scanning…' : 'Scan now'}
            </button>
            <button
              type="button"
              onClick={() => remove.mutate()}
              className="min-h-11 rounded-lg bg-surface px-4 text-sm text-text-2"
            >
              Disconnect
            </button>
          </>
        )}
      </div>

      {scan.data && (
        <p className="pt-2 text-xs text-text-2">
          {scan.data.subscriptions} subscriptions, {scan.data.videos} videos.
        </p>
      )}
    </SettingsSection>
  )
}
