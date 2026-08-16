import { useState } from 'react'
import { Link } from 'react-router-dom'
import { ListPlus, Trash2 } from 'lucide-react'
import {
  useCreatePlaylist,
  useDeletePlaylist,
  usePlaylists,
} from '@/features/catalog/application/queries'
import type { Playlist } from '@/features/catalog/domain/video'
import { ThumbnailSurface } from '@/shared/ui/primitives'
import { mediaURL } from '@/shared/lib/media'
import { useToast } from '@/shared/ui/toast'

/**
 * The member's own playlists.
 *
 * Per member, not per household — the same division the rest of the schema
 * already draws: videos and channels are everybody's, while what somebody
 * assembled is theirs. See CLAUDE.md §5.
 */
export function PlaylistsPage() {
  const { data: playlists, isPending, isError } = usePlaylists()
  const create = useCreatePlaylist()
  const [title, setTitle] = useState('')
  const toast = useToast()

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    const name = title.trim()
    if (!name) return
    create.mutate(name, {
      onSuccess: () => {
        setTitle('')
        toast('Playlist created')
      },
    })
  }

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <h1 className="py-4 text-2xl font-bold">Playlists</h1>

      <form onSubmit={submit} className="mb-8 flex gap-2">
        <input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="New playlist"
          aria-label="New playlist name"
          className="h-11 flex-1 rounded-xl bg-surface px-4 text-sm outline-none ring-1 ring-line focus:ring-2 focus:ring-text-2"
        />
        <button
          type="submit"
          disabled={!title.trim() || create.isPending}
          className="flex h-11 items-center gap-2 rounded-xl bg-surface px-4 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover disabled:opacity-50"
        >
          <ListPlus size={18} />
          Create
        </button>
      </form>

      {isError ? (
        <p className="py-16 text-center text-text-2">
          Could not load playlists. Is the gateway running?
        </p>
      ) : isPending ? (
        <p className="py-16 text-center text-text-2">Loading…</p>
      ) : playlists && playlists.length > 0 ? (
        <div className="grid grid-cols-1 gap-x-4 gap-y-8 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3 min-[1600px]:grid-cols-4">
          {playlists.map((p) => (
            <PlaylistCard key={p.id} playlist={p} />
          ))}
        </div>
      ) : (
        <p className="py-16 text-center text-text-2">
          No playlists yet. Name one above, then add videos from any video&rsquo;s menu.
        </p>
      )}
    </div>
  )
}

function PlaylistCard({ playlist }: { playlist: Playlist }) {
  const remove = useDeletePlaylist()
  const toast = useToast()
  const cover = playlist.thumbnails[0]

  return (
    <div className="group relative">
      <Link to={`/playlist/${playlist.id}`} className="block">
        <ThumbnailSurface>
          {cover ? (
            <img
              src={mediaURL(cover)}
              alt=""
              loading="lazy"
              className="h-full w-full object-cover"
            />
          ) : null}
        </ThumbnailSurface>
        <h2 className="mt-2 line-clamp-2 font-medium">{playlist.title}</h2>
        <p className="mt-0.5 text-sm text-text-2">
          {playlist.itemCount} {playlist.itemCount === 1 ? 'video' : 'videos'}
          {playlist.sourceUrl ? ' · from YouTube' : ''}
        </p>
      </Link>

      {/* Destructive and irreversible, so it asks. Everything else on this page
          can be undone by pressing it again; this one cannot. */}
      <button
        type="button"
        aria-label={`Delete ${playlist.title}`}
        onClick={() => {
          if (!window.confirm(`Delete "${playlist.title}"? The videos stay in the library.`)) {
            return
          }
          remove.mutate(playlist.id, { onSuccess: () => toast('Playlist deleted') })
        }}
        className="absolute top-2 right-2 grid h-9 w-9 place-items-center rounded-full bg-black/60 text-white opacity-0 transition-opacity duration-150 ease-out group-hover:opacity-100 focus-visible:opacity-100"
      >
        <Trash2 size={16} />
      </button>
    </div>
  )
}
