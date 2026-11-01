import clsx from 'clsx'
import { ChevronDown, ChevronUp, GripVertical, Pause, Play, SkipForward, Smartphone, Square, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { useQueryClient } from '@tanstack/react-query'
import { formatDuration } from '@/shared/lib/format'
import { mediaURL } from '@/shared/lib/media'
import { useToast } from '@/shared/ui/toast'
import { currentItem, endNeedsSaying, indexAfterEdit, moveItem, type CastSession } from '../domain/cast'
import { CastError } from '../infrastructure/cast-repository'
import { useCast } from '../application/cast-context'
import { useCastControls, useCastSession, useHandoff } from '../application/queries'

/**
 * The remote: what the speaker is playing, and everything that can be done to
 * it. It takes the screen while it is open — the video here is paused, and the
 * speaker is the player now.
 *
 * Closes itself when the session ends, and says why when the viewer did not
 * end it themselves (`endNeedsSaying`).
 */
export function CastRemote() {
  const { remoteSessionId, closeRemote } = useCast()
  const { data: session } = useCastSession(remoteSessionId)
  const { t } = useTranslation()
  const toast = useToast()

  useEffect(() => {
    if (!session || session.state !== 'ended') return
    if (endNeedsSaying(session.endedReason)) toast(t(`cast.ended.${session.endedReason}` as 'cast.ended.failed'))
    closeRemote()
  }, [session, closeRemote, toast, t])

  if (!remoteSessionId || !session || session.state === 'ended') return null
  return createPortal(<RemotePanel session={session} onClose={closeRemote} />, document.body)
}

function useCommandError() {
  const { t } = useTranslation()
  const toast = useToast()
  return (err: unknown) => {
    const code = err instanceof CastError ? err.code : 'unknown'
    toast(code === 'device_offline' ? t('cast.offline') : t('cast.commandFailed'))
  }
}

export function RemotePanel({ session, onClose }: { session: CastSession; onClose: () => void }) {
  const { t } = useTranslation()
  const controls = useCastControls(session.id)
  const handoff = useHandoff(session.id)
  const onError = useCommandError()
  const item = currentItem(session)

  const status =
    session.state === 'loading'
      ? t('cast.loadingOn', { device: session.device.name })
      : session.state === 'paused'
        ? t('cast.pausedOn', { device: session.device.name })
        : t('cast.playingOn', { device: session.device.name })

  const cover = mediaURL(item?.thumbnailUrl)
  const button =
    'grid size-12 place-items-center rounded-full text-text hover:bg-surface-hover disabled:opacity-40'

  return (
    <div
      className="fixed inset-0 z-[65] flex flex-col overflow-y-auto bg-bg"
      role="dialog"
      aria-modal="true"
      aria-label={status}
    >
      <div className="mx-auto flex w-full max-w-xl flex-col gap-4 px-4 pt-[max(1rem,env(safe-area-inset-top))] pb-[max(1.5rem,env(safe-area-inset-bottom))]">
        <header className="flex items-center gap-2">
          <p className="min-w-0 flex-1 truncate text-sm text-text-2">{status}</p>
          <button type="button" aria-label={t('cast.closeRemote')} onClick={onClose} className={button}>
            <X size={22} />
          </button>
        </header>

        <div className="aspect-video w-full overflow-hidden rounded-xl bg-surface">
          {cover && <img src={cover} alt="" className="size-full object-cover" />}
        </div>

        <div className="min-w-0">
          <h2 className="truncate text-lg font-medium text-text">{item?.title}</h2>
          <p className="truncate text-sm text-text-2">{item?.channel}</p>
        </div>

        <Seek session={session} onSeek={(s) => controls.seek.mutate(s, { onError })} />

        <div className="flex items-center justify-center gap-3">
          {session.state === 'paused' ? (
            <button type="button" aria-label={t('cast.play')} className={button}
              onClick={() => controls.resume.mutate(undefined, { onError })}>
              <Play size={26} />
            </button>
          ) : (
            <button type="button" aria-label={t('cast.pause')} className={button}
              disabled={session.state === 'loading'}
              onClick={() => controls.pause.mutate(undefined, { onError })}>
              <Pause size={26} />
            </button>
          )}
          <button type="button" aria-label={t('cast.next')} className={button}
            disabled={session.currentIndex >= session.queue.length - 1}
            onClick={() => controls.next.mutate(undefined, { onError })}>
            <SkipForward size={24} />
          </button>
          <button type="button" aria-label={t('cast.stop')} className={button}
            onClick={() => controls.stop.mutate(undefined, { onError })}>
            <Square size={22} />
          </button>
        </div>

        <Volume session={session} onVolume={(v) => controls.volume.mutate(v, { onError })} />

        <button
          type="button"
          onClick={() => handoff.mutate(undefined, { onError })}
          className="flex min-h-12 items-center justify-center gap-2 rounded-full bg-surface px-4 text-sm text-text hover:bg-surface-hover"
        >
          <Smartphone size={18} />
          {t('cast.handoff')}
        </button>

        <QueueList session={session} />
      </div>
    </div>
  )
}

/**
 * A slider that commits when let go. Sending every step of a drag would be a
 * seek per pixel to a speaker that reloads the song for each one.
 */
function useHeld(value: number) {
  const [held, setHeld] = useState<number | null>(null)
  return { shown: held ?? value, hold: setHeld, release: () => setHeld(null), held }
}

function Seek({ session, onSeek }: { session: CastSession; onSeek: (seconds: number) => void }) {
  const { t } = useTranslation()
  const { shown, hold, release, held } = useHeld(session.positionSeconds)
  const commit = () => {
    if (held !== null) onSeek(held)
    release()
  }
  const duration = Math.max(session.durationSeconds, 1)
  return (
    <div className="flex flex-col gap-1">
      <input
        type="range"
        aria-label={t('cast.seek')}
        min={0}
        max={duration}
        step={1}
        value={Math.min(shown, duration)}
        disabled={session.state === 'loading'}
        onChange={(e) => hold(Number(e.target.value))}
        onPointerUp={commit}
        onKeyUp={commit}
        className="w-full accent-brand"
      />
      <div className="flex justify-between text-xs text-text-2 tabular-nums">
        <span>{formatDuration(shown)}</span>
        <span>{formatDuration(session.durationSeconds)}</span>
      </div>
    </div>
  )
}

function Volume({ session, onVolume }: { session: CastSession; onVolume: (percent: number) => void }) {
  const { t } = useTranslation()
  const { shown, hold, release, held } = useHeld(session.volume)
  const commit = () => {
    if (held !== null) onVolume(held)
    release()
  }
  return (
    <label className="flex items-center gap-3 text-sm text-text-2">
      <span className="shrink-0">{t('cast.volume')}</span>
      <input
        type="range"
        aria-label={t('cast.volume')}
        min={0}
        max={100}
        step={1}
        value={shown}
        onChange={(e) => hold(Number(e.target.value))}
        onPointerUp={commit}
        onKeyUp={commit}
        className="w-full accent-brand"
      />
      <span className="w-8 shrink-0 text-right tabular-nums">{shown}</span>
    </label>
  )
}

function QueueList({ session }: { session: CastSession }) {
  const { t } = useTranslation()
  const toast = useToast()
  const queryClient = useQueryClient()
  const controls = useCastControls(session.id)
  const onError = useCommandError()
  const [dragging, setDragging] = useState<number | null>(null)

  const edit = (items: CastSession['queue']) =>
    controls.replaceQueue.mutate(
      { version: session.version, videoIds: items.map((it) => it.videoId), currentIndex: indexAfterEdit(session, items) },
      {
        onError: (err) => {
          if (err instanceof CastError && err.code === 'stale_version') {
            toast(t('cast.queueChanged'))
            void queryClient.invalidateQueries({ queryKey: ['cast', 'session', session.id] })
            return
          }
          onError(err)
        },
      },
    )

  const small = 'grid size-9 place-items-center rounded-full text-text-2 hover:bg-surface-hover disabled:opacity-30'

  return (
    <section className="flex flex-col gap-1">
      <h3 className="px-1 text-sm font-medium text-text">{t('cast.queue')}</h3>
      <ol>
        {session.queue.map((item, index) => {
          const current = index === session.currentIndex
          return (
            <li
              key={`${item.videoId}-${index}`}
              draggable
              onDragStart={() => setDragging(index)}
              onDragOver={(e) => e.preventDefault()}
              onDrop={() => {
                if (dragging !== null && dragging !== index) edit(moveItem(session.queue, dragging, index))
                setDragging(null)
              }}
              className={clsx('flex items-center gap-1 rounded-xl', current && 'bg-surface')}
            >
              <span className="grid w-6 shrink-0 place-items-center text-text-2" aria-hidden>
                <GripVertical size={16} />
              </span>
              <button
                type="button"
                aria-label={t('cast.jumpTo', { title: item.title })}
                aria-current={current ? 'true' : undefined}
                onClick={() => !current && controls.jump.mutate(index, { onError })}
                className="flex min-h-12 min-w-0 flex-1 items-center gap-3 py-1 text-left"
              >
                {mediaURL(item.thumbnailUrl) ? (
                  <img src={mediaURL(item.thumbnailUrl)} alt="" className="aspect-video w-20 shrink-0 rounded-md bg-surface object-cover" />
                ) : (
                  <span className="aspect-video w-20 shrink-0 rounded-md bg-surface" />
                )}
                <span className="min-w-0">
                  <span className={clsx('block truncate text-sm', current ? 'text-text font-medium' : 'text-text')}>
                    {item.title}
                  </span>
                  <span className="block truncate text-xs text-text-2">
                    {current ? t('cast.nowPlaying') : item.channel}
                  </span>
                </span>
              </button>
              <button type="button" aria-label={t('cast.moveUp', { title: item.title })} className={small}
                disabled={index === 0} onClick={() => edit(moveItem(session.queue, index, index - 1))}>
                <ChevronUp size={18} />
              </button>
              <button type="button" aria-label={t('cast.moveDown', { title: item.title })} className={small}
                disabled={index === session.queue.length - 1}
                onClick={() => edit(moveItem(session.queue, index, index + 1))}>
                <ChevronDown size={18} />
              </button>
              <button type="button" aria-label={t('cast.remove', { title: item.title })} className={small}
                disabled={session.queue.length === 1}
                onClick={() => edit(session.queue.filter((_, i) => i !== index))}>
                <X size={18} />
              </button>
            </li>
          )
        })}
      </ol>
    </section>
  )
}
