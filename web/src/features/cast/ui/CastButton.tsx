import { Cast, Speaker } from 'lucide-react'
import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import type { CastDevice } from '../domain/cast'
import { useCastDevices } from '../application/queries'

/**
 * The player's cast control.
 *
 * Drawn only when a speaker is answering: a button that opens an empty list is
 * a dead button (§5). The caller leaves it out for a broadcast, which has no
 * end for a speaker to play to.
 */
export function CastButton({
  className,
  onPick,
  disabled,
}: {
  className: string
  onPick: (device: CastDevice) => void
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const { data: devices } = useCastDevices()
  const [open, setOpen] = useState(false)

  if (!devices || devices.length === 0) return null
  return (
    <>
      <button
        type="button"
        aria-label={t('cast.button')}
        title={t('cast.button')}
        onClick={() => setOpen(true)}
        disabled={disabled}
        className={className}
      >
        <Cast size={20} />
      </button>
      {open && (
        <DevicePicker
          onClose={() => setOpen(false)}
          onPick={(device) => {
            setOpen(false)
            onPick(device)
          }}
        />
      )}
    </>
  )
}

/**
 * The speakers to choose from: a sheet from the bottom on a phone, a panel in
 * the middle elsewhere. Portalled to the body, because the player it is opened
 * from moves under a transform and a fixed element inside one is fixed to it.
 */
export function DevicePicker({ onPick, onClose }: { onPick: (device: CastDevice) => void; onClose: () => void }) {
  const { t } = useTranslation()
  const { data: devices, isFetching, refetch } = useCastDevices()

  // Asked again on opening: the list behind the button may be a minute old,
  // and a speaker unplugged since then should not be offered.
  useEffect(() => {
    void refetch()
  }, [refetch])

  useEffect(() => {
    const escape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [onClose])

  return createPortal(
    <div
      className="fixed inset-0 z-[70] grid place-items-end bg-black/60 min-[700px]:place-items-center min-[700px]:px-4"
      role="dialog"
      aria-modal="true"
      aria-label={t('cast.pickerTitle')}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div className="w-full rounded-t-2xl border border-line bg-surface p-4 pb-[max(1rem,env(safe-area-inset-bottom))] min-[700px]:max-w-sm min-[700px]:rounded-2xl">
        <h2 className="mb-2 px-2 text-base font-medium text-text">{t('cast.pickerTitle')}</h2>
        {!devices && isFetching && <p className="px-2 py-3 text-sm text-text-2">{t('cast.loadingDevices')}</p>}
        {devices && devices.length === 0 && <p className="px-2 py-3 text-sm text-text-2">{t('cast.pickerEmpty')}</p>}
        <ul>
          {devices?.map((device) => (
            <li key={device.id}>
              <button
                type="button"
                onClick={() => onPick(device)}
                className="flex min-h-12 w-full items-center gap-3 rounded-xl px-2 py-2 text-left hover:bg-surface-hover focus-visible:bg-surface-hover"
              >
                <span className="grid size-9 shrink-0 place-items-center rounded-full bg-surface-hover text-text">
                  {device.kind === 'r1' ? <Speaker size={18} /> : <Cast size={18} />}
                </span>
                <span className="min-w-0">
                  <span className="block truncate text-sm text-text">{device.name}</span>
                  <span className="block truncate text-xs text-text-2">
                    {device.kind === 'r1' ? t('cast.kindR1') : t('cast.kindGoogle')}
                  </span>
                </span>
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>,
    document.body,
  )
}
