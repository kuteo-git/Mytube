import { useState } from 'react'
import clsx from 'clsx'

import { useCurrentProfile, useProfiles } from '../application/use-profile'
import { httpProfileRepository } from '../infrastructure/profileRepository'
import { validProfileName, type Profile } from '../domain/profile'
import { DeleteProfileDialog } from './DeleteProfileDialog'

/**
 * Who is watching.
 *
 * Shown once, on a browser that has never chosen, and reachable afterwards from
 * Settings. Deliberately not a login screen: there is no password, nothing is
 * verified, and saying "Sign in" would promise a protection this does not have
 * (§3 — the LAN is trusted, and the video files are already unprotected).
 *
 * The names are large and the list is short because this is read from a sofa as
 * often as from a desk.
 */
export function ProfilePicker({
  onDone,
  manage = false,
}: {
  onDone?: () => void
  /**
   * Show what each profile holds, and offer to delete it.
   *
   * Off by default: the same component answers "who is watching?" at the gate,
   * where a row of delete buttons in front of somebody trying to start watching
   * would be both noise and a hazard.
   */
  manage?: boolean
}) {
  const { data: profiles = [], isLoading, refetch } = useProfiles()
  const { id: currentID, choose } = useCurrentProfile()
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [deleting, setDeleting] = useState<Profile | null>(null)

  const add = async () => {
    if (!validProfileName(name)) {
      setError('Enter a name')
      return
    }
    setBusy(true)
    setError('')
    try {
      const created = await httpProfileRepository.create(name.trim())
      await refetch()
      choose(created)
      onDone?.()
    } catch {
      setError('Could not add that name')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mx-auto w-full max-w-md px-4 py-10">
      <h1 className="text-xl font-medium">Who's watching?</h1>
      <p className="pt-1 text-sm text-text-2">
        Keeps subscriptions, history and recommendations separate. The library
        itself is shared.
      </p>

      {deleting && (
        <DeleteProfileDialog
          profile={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            setDeleting(null)
            void refetch()
          }}
        />
      )}

      {isLoading ? (
        <p className="pt-6 text-sm text-text-2">Loading…</p>
      ) : (
        <ul className="flex flex-col gap-2 pt-6">
          {profiles.map((p) => (
            <li key={p.id} className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => {
                  choose(p)
                  onDone?.()
                }}
                className={clsx(
                  'flex min-w-0 flex-1 items-center gap-3 rounded-xl px-4 py-3 text-left transition-colors duration-150 ease-out',
                  p.id === currentID
                    ? 'bg-invert-bg text-invert-text'
                    : 'bg-surface hover:bg-surface-hover',
                )}
              >
                <span
                  aria-hidden
                  className="grid h-10 w-10 shrink-0 place-items-center rounded-full bg-white/10 text-sm font-medium"
                >
                  {p.name.slice(0, 1).toUpperCase()}
                </span>
                <span className="min-w-0 flex-1 truncate text-base">{p.name}</span>
              </button>

              {/* Never for the profile in use, and never for the last one — the
                  gateway refuses both, and a button that exists only to be
                  refused is the dead control §5 forbids. Drawn rather than
                  hidden behind an "Edit" mode: the confirmation carries the real
                  numbers, which is the guard, and a mode is one more state to
                  remember. */}
              {manage && p.id !== currentID && profiles.length > 1 && (
                <button
                  type="button"
                  aria-label={`Delete ${p.name}`}
                  onClick={() => setDeleting(p)}
                  className="shrink-0 rounded-full px-3 py-2 text-sm text-text-2 transition-colors duration-150 ease-out hover:bg-surface-hover hover:text-text"
                >
                  Delete
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      {adding ? (
        <div className="flex flex-col gap-2 pt-4">
          <input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void add()
            }}
            placeholder="Name"
            aria-label="New profile name"
            maxLength={40}
            className="w-full rounded-lg bg-surface-input px-4 py-3 text-base outline-none ring-1 ring-border focus:ring-2 focus:ring-brand"
          />
          {error && <p className="text-xs text-brand">{error}</p>}
          <button
            type="button"
            disabled={busy}
            onClick={() => void add()}
            className="min-h-11 rounded-lg bg-invert-bg px-4 text-sm font-medium text-invert-text disabled:opacity-50"
          >
            {busy ? 'Adding…' : 'Add'}
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setAdding(true)}
          className="mt-4 min-h-11 w-full rounded-xl bg-surface px-4 text-sm text-text-2 transition-colors duration-150 ease-out hover:bg-surface-hover"
        >
          Add someone
        </button>
      )}
    </div>
  )
}
