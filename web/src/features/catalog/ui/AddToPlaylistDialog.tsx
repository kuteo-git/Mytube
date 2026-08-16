import { useState } from 'react'
import { createPortal } from 'react-dom'
import { Check, ListPlus, X } from 'lucide-react'
import {
  useCreatePlaylist,
  usePlaylists,
  useSetPlaylistItem,
} from '@/features/catalog/application/queries'
import { useToast } from '@/shared/ui/toast'

/**
 * "Add to playlist" for one video.
 *
 * A dialog rather than a submenu on the card's own menu: the card menu is
 * anchored inside a thumbnail that clips, the list of playlists is unbounded,
 * and this has to work under a finger as well as a pointer. It also has to
 * offer creating one — otherwise the first time anybody presses it there is
 * nothing to choose.
 *
 * Portals to the body for the same reason every other panel here does: the grid
 * clips its overflow.
 */
export function AddToPlaylistDialog({
  videoId,
  onClose,
}: {
  videoId: string
  onClose: () => void
}) {
  const { data: playlists, isPending } = usePlaylists()
  const setItem = useSetPlaylistItem()
  const create = useCreatePlaylist()
  const [title, setTitle] = useState('')
  const [added, setAdded] = useState<string[]>([])
  const toast = useToast()

  const add = (playlistId: string) => {
    setItem.mutate(
      { playlistId, videoId, included: true },
      {
        onSuccess: () => setAdded((ids) => [...ids, playlistId]),
        onError: () => toast('Could not add to that playlist'),
      },
    )
  }

  const createAndAdd = (e: React.FormEvent) => {
    e.preventDefault()
    const name = title.trim()
    if (!name) return
    create.mutate(name, {
      onSuccess: (playlist) => {
        setTitle('')
        add(playlist.id)
      },
    })
  }

  return createPortal(
    <div
      className="fixed inset-0 z-50 grid place-items-center bg-black/50 p-4"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-label="Add to playlist"
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-sm overflow-hidden rounded-2xl bg-surface shadow-xl ring-1 ring-line"
      >
        <div className="flex items-center justify-between px-4 py-3">
          <h2 className="text-sm font-medium">Add to playlist</h2>
          <button
            type="button"
            aria-label="Close"
            onClick={onClose}
            className="grid h-8 w-8 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            <X size={16} />
          </button>
        </div>

        <ul className="max-h-64 overflow-y-auto">
          {isPending && <li className="px-4 py-2.5 text-sm text-text-2">Loading…</li>}
          {playlists?.map((p) => {
            const done = added.includes(p.id)
            return (
              <li key={p.id}>
                <button
                  type="button"
                  disabled={done}
                  onClick={() => add(p.id)}
                  className="flex w-full items-center justify-between gap-3 px-4 py-2.5 text-left text-sm transition-colors duration-150 ease-out hover:bg-surface-hover disabled:text-text-2"
                >
                  <span className="truncate">{p.title}</span>
                  {done ? <Check size={16} /> : <span className="text-text-2">{p.itemCount}</span>}
                </button>
              </li>
            )
          })}
          {playlists?.length === 0 && (
            <li className="px-4 py-2.5 text-sm text-text-2">No playlists yet.</li>
          )}
        </ul>

        <form onSubmit={createAndAdd} className="flex gap-2 border-t border-line p-3">
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="New playlist"
            aria-label="New playlist name"
            className="h-10 flex-1 rounded-lg bg-bg px-3 text-sm outline-none ring-1 ring-line focus:ring-2 focus:ring-text-2"
          />
          <button
            type="submit"
            disabled={!title.trim() || create.isPending}
            className="grid h-10 w-10 place-items-center rounded-lg bg-bg transition-colors duration-150 ease-out hover:bg-surface-hover disabled:opacity-50"
            aria-label="Create and add"
          >
            <ListPlus size={18} />
          </button>
        </form>
      </div>
    </div>,
    document.body,
  )
}
