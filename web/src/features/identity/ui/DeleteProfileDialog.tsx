import { useEffect, useState } from 'react'

import { httpProfileRepository, type ProfileUsage } from '../infrastructure/profileRepository'
import type { Profile } from '../domain/profile'

/**
 * What deleting a profile takes with it, said in numbers before it happens.
 *
 * "Delete profile" sounds far lighter than what it does — on this household's
 * largest member it is 351 subscriptions, 889 watched videos, 27 playlists and
 * 63,908 ranking signals. A dialog that only asks "are you sure?" is asking
 * about a word rather than about the thing.
 *
 * The numbers come from the same query that then does the deleting, run with
 * `dry_run`. Two queries — one to count, one to delete — would be two
 * definitions of "what belongs to this profile", agreeing right up until the
 * day one of them is changed.
 *
 * No type-the-name step. That is the ceremony for dropping a database; this is
 * a house of five, and the numbers above are already the pause.
 */
export function DeleteProfileDialog({
  profile,
  onClose,
  onDeleted,
}: {
  profile: Profile
  onClose: () => void
  onDeleted: () => void
}) {
  const [usage, setUsage] = useState<ProfileUsage | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let gone = false
    httpProfileRepository
      .usage(profile.id)
      .then((u) => {
        if (!gone) setUsage(u)
      })
      .catch(() => {
        if (!gone) setError('Could not read what this profile holds.')
      })
    return () => {
      gone = true
    }
  }, [profile.id])

  useEffect(() => {
    const escape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [onClose])

  const remove = async () => {
    setBusy(true)
    setError('')
    try {
      await httpProfileRepository.remove(profile.id)
      onDeleted()
    } catch (e) {
      // The gateway refuses two cases in words worth showing as they are: the
      // profile you are using, and the last one left.
      setError(e instanceof Error && e.message ? e.message : 'Could not delete that profile.')
      setBusy(false)
    }
  }

  // Only what this profile actually has. A list padded with "0 playlists" reads
  // as a form rather than as a warning, and the zeroes are the parts nobody
  // needs to think about.
  const lines = usage
    ? ([
        [usage.subscriptions, 'subscriptions'],
        [usage.watched, 'videos watched'],
        [usage.playlists, 'playlists'],
        [usage.reactions, 'likes and dislikes'],
        [usage.saved, 'saved videos'],
        [usage.watchLater, 'in Watch later'],
        [usage.comments, 'comments'],
      ] as const).filter(([n]) => n > 0)
    : []

  return (
    <div
      className="fixed inset-0 z-[60] grid place-items-center bg-black/60 px-4"
      role="dialog"
      aria-modal="true"
      aria-label={`Delete ${profile.name}`}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div className="w-full max-w-sm rounded-2xl border border-line bg-surface p-5">
        <h2 className="text-lg font-medium">Delete {profile.name}?</h2>

        {usage === null && !error ? (
          <p className="pt-2 text-sm text-text-2">Counting what this profile holds…</p>
        ) : lines.length > 0 ? (
          <>
            <p className="pt-2 text-sm text-text-2">This removes, for {profile.name} only:</p>
            <ul className="pt-2 text-sm">
              {lines.map(([n, label]) => (
                <li key={label} className="py-0.5">
                  <span className="font-medium">{n.toLocaleString()}</span> {label}
                </li>
              ))}
            </ul>
          </>
        ) : (
          <p className="pt-2 text-sm text-text-2">
            This profile has not watched or saved anything yet.
          </p>
        )}

        {/* The reassurance is as important as the warning: people hesitate here
            because they think the videos go too. They do not — the library is
            shared, and only this person's side of it is being removed. */}
        <p className="pt-3 text-sm text-text-2">
          The videos and channels themselves stay — they belong to the whole
          household. This cannot be undone.
        </p>

        {error && <p className="pt-3 text-sm text-brand">{error}</p>}

        <div className="flex justify-end gap-2 pt-5">
          <button
            type="button"
            onClick={onClose}
            className="rounded-full px-4 py-2 text-sm transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={() => void remove()}
            disabled={busy}
            className="rounded-full bg-brand px-4 py-2 text-sm font-medium text-white transition-colors duration-150 ease-out disabled:opacity-60"
          >
            {busy ? 'Deleting…' : 'Delete'}
          </button>
        </div>
      </div>
    </div>
  )
}
