import { Pencil, RefreshCw } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

/**
 * Choosing one model out of a couple of hundred.
 *
 * A plain select is what the reference design uses, and it is right for a
 * handful. This router answers with 212, where scrolling to find one by eye is
 * the wrong interaction — people know a fragment of the name ("sub", "gemini")
 * and want to type it. So the field filters as you type.
 *
 * The pencil switches to free text, kept from the reference for the case the
 * list cannot cover: a model released between now and whenever the provider
 * updates what it advertises.
 */
export function ModelPicker({
  value,
  models,
  loading,
  onChange,
  onRefresh,
}: {
  value: string
  models: string[]
  loading: boolean
  onChange: (v: string) => void
  onRefresh: () => void
}) {
  const { t } = useTranslation()
  const [filter, setFilter] = useState('')
  const [manual, setManual] = useState(false)
  const [open, setOpen] = useState(false)

  const matches = useMemo(() => {
    const q = filter.trim().toLowerCase()
    const list = q ? models.filter((m) => m.toLowerCase().includes(q)) : models
    // Enough to scan, not so many the page grows a second scrollbar.
    return list.slice(0, 50)
  }, [models, filter])

  if (manual) {
    return (
      <>
        <input
          className="min-w-0 flex-1 rounded-lg bg-surface-input px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"
          value={value}
          placeholder={t('settings.model.name')}
          aria-label={t('ui.model')}
          onChange={(e) => onChange(e.target.value)}
        />
        <IconButton label={t('settings.model.chooseFromList')} onClick={() => setManual(false)}>
          <RefreshCw size={16} />
        </IconButton>
      </>
    )
  }

  return (
    <>
      <div className="relative min-w-0 flex-1">
        <input
          className="w-full rounded-lg bg-surface-input px-3 py-2 text-sm outline-none focus:ring-1 focus:ring-ring"
          value={open ? filter : value}
          placeholder={models.length ? 'type to filter…' : 'refresh to load models'}
          aria-label={t('ui.model')}
          onFocus={() => {
            setFilter('')
            setOpen(true)
          }}
          // A blur that closes immediately would fire before the click on an
          // option had a chance to land.
          onBlur={() => window.setTimeout(() => setOpen(false), 120)}
          onChange={(e) => {
            setFilter(e.target.value)
            setOpen(true)
          }}
        />
        {open && models.length > 0 && (
          <ul className="absolute z-20 mt-1 max-h-60 w-full overflow-y-auto rounded-lg bg-surface py-1 text-sm shadow-lg">
            {matches.length === 0 && (
              <li className="px-3 py-2 text-text-2">{t('settings.model.noMatch')}</li>
            )}
            {matches.map((m) => (
              <li key={m}>
                <button
                  type="button"
                  className={
                    'w-full px-3 py-2 text-left transition-colors duration-150 ease-out hover:bg-surface-hover ' +
                    (m === value ? 'font-medium' : '')
                  }
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => {
                    onChange(m)
                    setOpen(false)
                  }}
                >
                  {m}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      <IconButton label={t('settings.model.reload')} onClick={onRefresh} busy={loading}>
        <RefreshCw size={16} className={loading ? 'animate-spin' : undefined} />
      </IconButton>
      <IconButton label={t('settings.model.type')} onClick={() => setManual(true)}>
        <Pencil size={16} />
      </IconButton>
    </>
  )
}

function IconButton({
  label,
  onClick,
  busy,
  children,
}: {
  label: string
  onClick: () => void
  busy?: boolean
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      disabled={busy}
      onClick={onClick}
      // 44px on every pointer: this row sits next to a text field on a phone,
      // and a 32px target beside one is the shape that gets mistapped.
      className="grid h-11 w-11 shrink-0 place-items-center rounded-lg bg-surface-hover text-text-2 transition-colors duration-150 ease-out hover:text-text disabled:opacity-50"
    >
      {children}
    </button>
  )
}
