import { useEffect, useMemo, useState } from 'react'
import { Bookmark, ListVideo, Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  useCreatePlaylist,
  usePlaylists,
  useSetInPlaylist,
  useSetPinned,
} from '../application/queries'

/**
 * Which collections this video belongs in.
 *
 * The bookmark used to write one bit — *keep this file when the disk fills* —
 * and there was nowhere to say **which** collection, so a household could not
 * keep music apart from news. Pressing it opens this.
 *
 * # Why one call and not two
 *
 * `GET /api/playlists?videoId=` answers the lists *and* which of them already
 * hold this video. The alternatives were costed on the phone first and refused
 * for the same reasons here: a second route for the ticks is a second request
 * for a dialog that is useless without the first, and merging on the client is
 * N requests for one bit each.
 *
 * # The first row is not a playlist
 *
 * It is the saved shelf — the pinned set — which is not a row in `playlists`.
 * That is *why* it cannot be renamed or deleted, rather than a rule invented
 * for this dialog. Modelling it as a playlist with a made-up id would put an
 * entry in the list that no call can reach, and make every call site remember
 * which id was magic.
 *
 * # Save applies the difference
 *
 * One request per change, not one call carrying the final state. A tap is one
 * or two changes, and an endpoint that takes the whole state is one that empties
 * a playlist the day a client is wrong about what was in it. Unticking really
 * removes: a tick that does not is a control that lies.
 */
export function SaveToPlaylistDialog({
  videoId,
  saved,
  onClose,
}: {
  videoId: string
  /** Whether the video is already on the saved shelf, as the caller knows it. */
  saved: boolean
  onClose: () => void
}) {
  const { t } = useTranslation()
  const { data: playlists, isPending, isError } = usePlaylists(videoId)
  const setPinned = useSetPinned()
  const setInPlaylist = useSetInPlaylist()
  const createPlaylist = useCreatePlaylist()

  // What the server said, and what the ticks now say. The difference between
  // them is what Save sends.
  const [ticks, setTicks] = useState<Record<string, boolean> | null>(null)
  const [pinnedTick, setPinnedTick] = useState(saved)
  const [naming, setNaming] = useState(false)
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)

  const baseline = useMemo(() => {
    const map: Record<string, boolean> = {}
    for (const p of playlists ?? []) map[p.id] = p.containsVideo
    return map
  }, [playlists])

  // Filled once the lists arrive, and never again — the answer is the starting
  // point, not a stream of updates that would undo ticks as somebody made them.
  useEffect(() => {
    if (playlists && ticks === null) setTicks(baseline)
  }, [playlists, ticks, baseline])

  useEffect(() => {
    const escape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [onClose])

  const current = ticks ?? baseline
  const changed =
    pinnedTick !== saved ||
    Object.keys(current).some((id) => current[id] !== baseline[id])

  const create = async () => {
    const title = name.trim()
    if (!title) return
    setBusy(true)
    try {
      // Created **and** the video put in it, in one press. Leaving the add to a
      // later press of Save would make a named-but-empty playlist the outcome of
      // cancelling — and that press is not even on screen yet.
      const made = await createPlaylist.mutateAsync({ title })
      await setInPlaylist.mutateAsync({ id: made.id, videoId, member: true })
      setTicks({ ...current, [made.id]: true })
      setName('')
      setNaming(false)
    } finally {
      setBusy(false)
    }
  }

  const save = async () => {
    setBusy(true)
    try {
      if (pinnedTick !== saved) {
        await setPinned.mutateAsync({ videoId, pinned: pinnedTick })
      }
      for (const id of Object.keys(current)) {
        if (current[id] === baseline[id]) continue
        await setInPlaylist.mutateAsync({ id, videoId, member: current[id] })
      }
      onClose()
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      className="fixed inset-0 z-[60] grid place-items-center bg-black/60 px-4"
      role="dialog"
      aria-modal="true"
      aria-label={t('playlists.saveTo')}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div className="w-full max-w-sm rounded-2xl border border-line bg-surface p-5">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-medium">{t('playlists.saveTo')}</h2>
          <button
            type="button"
            onClick={() => setNaming(true)}
            aria-label={t('playlists.newPlaylist')}
            className="grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            <Plus size={18} />
          </button>
        </div>

        {naming && (
          <div className="flex gap-2 pt-3">
            <input
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                // Return confirms. Without it the key does nothing and the
                // button that was always going to be pressed next is one more
                // reach away.
                if (e.key === 'Enter') void create()
              }}
              placeholder={t('playlists.namePlaceholder')}
              className="min-w-0 flex-1 rounded-lg border border-line bg-bg px-3 py-2 text-sm outline-none focus:border-text-2"
            />
            <button
              type="button"
              disabled={!name.trim() || busy}
              onClick={() => void create()}
              className="rounded-lg bg-text px-3 py-2 text-sm font-medium text-bg disabled:opacity-40"
            >
              {t('common.create')}
            </button>
          </div>
        )}

        {isError ? (
          <p className="py-6 text-sm text-text-2">
            {t('empty.couldNotLoad', { what: t('empty.what_playlists') })}
          </p>
        ) : (
          <ul className="max-h-[45vh] overflow-y-auto py-3">
            {/* The shelf, first and fixed. */}
            <Row
              label={t('nav.saved')}
              icon={<Bookmark size={16} />}
              ticked={pinnedTick}
              onToggle={() => setPinnedTick((v) => !v)}
            />
            {isPending
              ? null
              : (playlists ?? []).map((p) => (
                  <Row
                    key={p.id}
                    label={p.title}
                    detail={t('playlists.count', { count: p.itemCount })}
                    icon={<ListVideo size={16} />}
                    ticked={Boolean(current[p.id])}
                    onToggle={() =>
                      setTicks({ ...current, [p.id]: !current[p.id] })
                    }
                  />
                ))}
          </ul>
        )}

        <div className="flex justify-end gap-2 pt-2">
          <button
            type="button"
            onClick={onClose}
            className="rounded-lg px-3 py-2 text-sm hover:bg-surface-hover"
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            // Nothing to apply means nothing to press. A Save that sends no
            // requests and closes the dialog looks exactly like one that
            // worked, which is how a control that does nothing goes unnoticed.
            disabled={!changed || busy}
            onClick={() => void save()}
            className="rounded-lg bg-text px-3 py-2 text-sm font-medium text-bg disabled:opacity-40"
          >
            {t('common.save')}
          </button>
        </div>
      </div>
    </div>
  )
}

function Row({
  label,
  detail,
  icon,
  ticked,
  onToggle,
}: {
  label: string
  detail?: string
  icon: React.ReactNode
  ticked: boolean
  onToggle: () => void
}) {
  return (
    <li>
      <button
        type="button"
        onClick={onToggle}
        aria-pressed={ticked}
        className="flex w-full items-center gap-3 rounded-lg px-2 py-2.5 text-left transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <span className="grid h-9 w-9 shrink-0 place-items-center rounded-full bg-surface-hover">
          {icon}
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm">{label}</span>
          {detail && <span className="block text-xs text-text-2">{detail}</span>}
        </span>
        {/* A box that is filled or empty, not a shade of the row. The phone
            learned this on its Like button: two surfaces six units apart is
            invisible as a state. */}
        <span
          aria-hidden
          className={
            ticked
              ? 'h-4 w-4 shrink-0 rounded border-2 border-text bg-text'
              : 'h-4 w-4 shrink-0 rounded border-2 border-text-2'
          }
        />
      </button>
    </li>
  )
}
