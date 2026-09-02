import {} from 'react-router-dom'
import { useState } from 'react'
import { MoreVertical, Plus } from 'lucide-react'
import {
  useCreatePlaylist,
  useDeletePlaylist,
  usePlaylists,
  useRenamePlaylist,
} from '@/features/catalog/application/queries'
import { useAccountState } from '@/features/settings/application/account-state'
import type { Playlist } from '@/features/catalog/domain/video'
import { ThumbnailSurface } from '@/shared/ui/primitives'
import { hueFromId } from '@/shared/lib/hue'
import { mediaURL } from '@/shared/lib/media'
import { useTranslation } from 'react-i18next'
import { PageLink } from '@/shared/ui/PageLink'
import { PageHeading } from '@/shared/ui/PageHeading'
import { ConfirmDialog, PromptDialog } from '@/shared/ui/dialog'

/**
 * The member's collections.
 *
 * **Two sources, one list.** Some arrive from a YouTube account on a scan and
 * some are made here, and once a row exists the difference stops mattering:
 * both can be renamed, emptied and deleted, because both are rows in this
 * household's own `playlists` table.
 *
 * That is new. This page was read-only and said so at length, on the sound
 * reasoning that a write would be undone by the next account scan — the gateway
 * simply had no routes to write with. It has them now.
 *
 * Per member: the same division the rest of the schema draws, where videos and
 * channels are the household's and what somebody assembled is theirs.
 */
export function PlaylistsPage() {
  const { t } = useTranslation()
  const { data: playlists, isPending, isError } = usePlaylists()
  // "Not read yet" becomes a lie the moment the session dies: nothing is coming
  // on the next pass, because there will not be one until somebody pastes a
  // fresh session. This is the screen where that is noticed.
  const { signedOut } = useAccountState()
  const createPlaylist = useCreatePlaylist()
  const [naming, setNaming] = useState(false)

  return (
    <div className="px-4 pb-16 min-[700px]:px-6">
      <div className="flex items-center justify-between gap-3">
        <PageHeading>{t('nav.playlists')}</PageHeading>
        <button
          type="button"
          onClick={() => setNaming(true)}
          className="flex shrink-0 items-center gap-2 rounded-full bg-surface px-4 py-2 text-sm hover:bg-surface-hover"
        >
          <Plus size={16} />
          {t('playlists.newPlaylist')}
        </button>
      </div>

      {naming && (
        <PromptDialog
          title={t('playlists.newPlaylist')}
          confirmLabel={t('common.create')}
          placeholder={t('playlists.namePlaceholder')}
          onConfirm={(title) => createPlaylist.mutate({ title })}
          onClose={() => setNaming(false)}
        />
      )}

      {isError ? (
        <p className="py-16 text-center text-text-2">
          {t('empty.couldNotLoad', { what: t('empty.what_playlists') })}
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
          {t('more.noPlaylistsYet')}
        </p>
      )}
    </div>
  )
}

function PlaylistCard({ playlist, signedOut }: { playlist: Playlist; signedOut: boolean }) {
  const { t } = useTranslation()
  const cover = playlist.thumbnails[0]
  const rename = useRenamePlaylist()
  const remove = useDeletePlaylist()
  const [renaming, setRenaming] = useState(false)
  const [deleting, setDeleting] = useState(false)

  return (
    <div className="group relative">
      <PlaylistMenu onRename={() => setRenaming(true)} onDelete={() => setDeleting(true)} />

      {/* Both are dialogs now, and the delete one is asked before it happens
          because there is nothing to undo. It was `window.confirm`, which works
          and is the *browser's* dialog rather than this app's — another
          typeface, another button order, the page's URL printed above the
          question, and on a television a system alert on a screen with no
          keyboard. */}
      {renaming && (
        <PromptDialog
          title={t('common.rename')}
          confirmLabel={t('common.save')}
          initial={playlist.title}
          placeholder={t('playlists.namePlaceholder')}
          onConfirm={(next) => rename.mutate({ id: playlist.id, title: next })}
          onClose={() => setRenaming(false)}
        />
      )}
      {deleting && (
        <ConfirmDialog
          title={t('playlists.deleteTitle')}
          detail={t('playlists.deleteDetail')}
          confirmLabel={t('common.delete')}
          onConfirm={() => remove.mutate({ id: playlist.id })}
          onClose={() => setDeleting(false)}
        />
      )}
      <PageLink to={`/playlist/${playlist.id}`} className="block">
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
          ? t('pages.playlists.wontOpen')
          : playlist.itemsSynced
            ? t('more.videosCount', { count: playlist.itemCount })
            : signedOut
              ? t('pages.playlists.waitingSession')
              : t('pages.playlists.notReadYet')}
      </p>
      </PageLink>
    </div>
  )
}

/** Rename and delete, on the card they act on. */
function PlaylistMenu({ onRename, onDelete }: { onRename: () => void; onDelete: () => void }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  return (
    <div className="absolute top-2 right-2 z-10">
      <button
        type="button"
        aria-label={t('common.moreOptions')}
        onClick={(e) => {
          // The card is a link, and a menu drawn on top of one is still inside
          // it: without this, opening the menu also opens the playlist.
          e.preventDefault()
          setOpen((o) => !o)
        }}
        className="grid h-8 w-8 place-items-center rounded-full bg-black/60 opacity-0 transition-opacity duration-150 group-hover:opacity-100 focus-visible:opacity-100"
      >
        <MoreVertical size={16} />
      </button>
      {open && (
        <ul className="absolute right-0 mt-1 min-w-40 overflow-hidden rounded-lg bg-surface py-1 text-sm shadow-lg ring-1 ring-line">
          <li>
            <button
              type="button"
              onClick={(e) => {
                e.preventDefault()
                setOpen(false)
                onRename()
              }}
              className="w-full px-4 py-2.5 text-left hover:bg-surface-hover"
            >
              {t('common.rename')}
            </button>
          </li>
          <li>
            <button
              type="button"
              onClick={(e) => {
                e.preventDefault()
                setOpen(false)
                onDelete()
              }}
              className="w-full px-4 py-2.5 text-left hover:bg-surface-hover"
            >
              {t('common.delete')}
            </button>
          </li>
        </ul>
      )}
    </div>
  )
}
