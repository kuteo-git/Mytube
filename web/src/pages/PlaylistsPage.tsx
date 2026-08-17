import { Link } from 'react-router-dom'
import { usePlaylists } from '@/features/catalog/application/queries'
import { useAccountState } from '@/features/settings/application/account-state'
import type { Playlist } from '@/features/catalog/domain/video'
import { ThumbnailSurface } from '@/shared/ui/primitives'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'

/**
 * The member's playlists, as their YouTube account has them.
 *
 * Read-only, and that is the whole design. These are a mirror refreshed on every
 * account scan, so a rename or a deletion made here would be silently undone by
 * the next pass — §5's rule against a control that does not do what it says,
 * reached through a control that appears to work for an hour.
 *
 * Per member: the same division the rest of the schema draws, where videos and
 * channels are the household's and what somebody assembled is theirs.
 */
export function PlaylistsPage() {
  const { data: playlists, isPending, isError } = usePlaylists()
  // "Not read yet" becomes a lie the moment the session dies: nothing is coming
  // on the next pass, because there will not be one until somebody pastes a
  // fresh session. This is the screen where that is noticed.
  const { signedOut } = useAccountState()

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <h1 className="py-4 text-2xl font-bold">Playlists</h1>

      {isError ? (
        <p className="py-16 text-center text-text-2">
          Could not load playlists. Is the gateway running?
        </p>
      ) : isPending ? (
        <div className="grid grid-cols-2 gap-x-4 gap-y-8 min-[700px]:grid-cols-3 min-[1000px]:grid-cols-4 min-[1600px]:grid-cols-5">
          {Array.from({ length: 8 }, (_, i) => (
            <div key={i}>
              {/* A plain surface, the same shape `VideoCardSkeleton` uses, not a
                  ThumbnailSurface. That one paints a gradient from a hue, and a
                  skeleton has no id to derive one from — so eight of them would
                  flash eight arbitrary colours for the moment before the real
                  covers arrive. A placeholder should be quiet. */}
              <div className="aspect-video w-full rounded-xl bg-surface" />
              <div className="mt-2 h-4 w-3/4 rounded bg-surface" />
            </div>
          ))}
        </div>
      ) : playlists && playlists.length > 0 ? (
        <div className="grid grid-cols-2 gap-x-4 gap-y-8 min-[700px]:grid-cols-3 min-[1000px]:grid-cols-4 min-[1600px]:grid-cols-5">
          {playlists.map((p) => (
            <PlaylistCard key={p.id} playlist={p} signedOut={signedOut} />
          ))}
        </div>
      ) : (
        <p className="py-16 text-center text-text-2">
          No playlists yet. Connect your YouTube account in Settings and they arrive on
          the next scan.
        </p>
      )}
    </div>
  )
}

function PlaylistCard({ playlist, signedOut }: { playlist: Playlist; signedOut: boolean }) {
  const cover = playlist.thumbnails[0]

  return (
    <Link to={`/playlist/${playlist.id}`} className="block">
      {/* Keyed on the playlist's own id, like every other card in the app. It
          matters more here than elsewhere: a playlist whose contents have not
          been read yet has no cover at all, so the gradient is the whole of what
          distinguishes one from another. */}
      <ThumbnailSurface hue={hueFromId(playlist.id)}>
        {cover ? (
          <img src={mediaURL(cover)} alt="" loading="lazy" className="h-full w-full object-cover" />
        ) : null}
      </ThumbnailSurface>
      <h2 className="mt-2 line-clamp-2 text-sm font-medium min-[700px]:text-base">
        {playlist.title}
      </h2>
      <p className="mt-0.5 text-xs text-text-2 min-[700px]:text-sm">
        {/* A playlist whose contents have not been read yet is empty, and an
            empty playlist reads as broken rather than pending. Contents are
            read a few per account scan — see CLAUDE.md §5 — so the wait is
            hours, and saying so is the difference between "not yet" and
            "something is wrong". */}
        {playlist.unavailable
          ? 'YouTube will not open this one'
          : playlist.itemsSynced
            ? `${playlist.itemCount} ${playlist.itemCount === 1 ? 'video' : 'videos'}`
            : signedOut
              ? 'Waiting for a YouTube session'
              : 'Not read yet'}
      </p>
    </Link>
  )
}
