import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

/**
 * The shell every dialog in this app shares.
 *
 * Three pieces, and each one is a thing a dialog is broken without: Escape
 * closes it, a click on the ground outside closes it, and it is announced as a
 * modal so a screen reader stops reading the page behind it. Written once
 * because they were about to be written a third time — `DeleteProfileDialog`
 * carries its own copy, and a second copy is how two dialogs come to disagree
 * about which of the three they have.
 *
 * The ground is `bg-black/60` rather than a blur: what is behind a dialog here
 * is a page of thumbnails, and dimming is what says "not this, that" without
 * costing a frame of compositing on a television.
 */
export function Dialog({
  label,
  onClose,
  children,
}: {
  /** What this dialog is, for the accessibility tree. */
  label: string
  onClose: () => void
  children: React.ReactNode
}) {
  useEffect(() => {
    const escape = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [onClose])

  return (
    <div
      className="fixed inset-0 z-[60] grid place-items-center bg-black/60 px-4"
      role="dialog"
      aria-modal="true"
      aria-label={label}
      onClick={(e) => {
        // The ground only. Without the check, a click that started on a button
        // inside and drifted a pixel closes the dialog under the finger.
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div className="w-full max-w-sm rounded-2xl border border-line bg-surface p-5">
        {children}
      </div>
    </div>
  )
}

/**
 * A dialog that asks for one word.
 *
 * Naming a playlist and renaming one are the same question with a different
 * starting value, so they are one component rather than two screens that will
 * eventually differ over whether Return submits.
 */
export function PromptDialog({
  title,
  confirmLabel,
  initial = '',
  placeholder,
  onConfirm,
  onClose,
}: {
  title: string
  confirmLabel: string
  initial?: string
  placeholder: string
  onConfirm: (value: string) => void
  onClose: () => void
}) {
  const { t } = useTranslation()
  const [value, setValue] = useState(initial)
  const ready = value.trim().length > 0

  const confirm = () => {
    if (!ready) return
    onConfirm(value.trim())
    onClose()
  }

  return (
    <Dialog label={title} onClose={onClose}>
      <h2 className="text-lg font-medium">{title}</h2>
      <input
        // Focused on arrival: a dialog whose only content is a field and which
        // does not put the caret in it asks for a click before it can be
        // answered.
        autoFocus
        value={value}
        // The caret at the end of a prefilled name, not in front of it. A
        // rename that opens with the caret at zero puts the first letter typed
        // before the word being edited — measured on the phone, where "test"
        // became "xtest".
        onFocus={(e) => e.currentTarget.setSelectionRange(value.length, value.length)}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          // Return confirms. Without it the key does nothing and the button
          // that was always going to be pressed next is one more reach away.
          if (e.key === 'Enter') confirm()
        }}
        placeholder={placeholder}
        className="mt-3 w-full rounded-lg border border-line bg-bg px-3 py-2 text-sm outline-none focus:border-text-2"
      />
      <div className="flex justify-end gap-2 pt-4">
        <button
          type="button"
          onClick={onClose}
          className="rounded-lg px-3 py-2 text-sm hover:bg-surface-hover"
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          disabled={!ready}
          onClick={confirm}
          className="rounded-lg bg-text px-3 py-2 text-sm font-medium text-bg disabled:opacity-40"
        >
          {confirmLabel}
        </button>
      </div>
    </Dialog>
  )
}

/**
 * A dialog that asks before something that cannot be undone.
 *
 * It replaced `window.confirm`, which works and is the browser's dialog rather
 * than this app's — a different typeface, a different button order, and the
 * URL of the page printed above the question. On a television it is worse than
 * that: it is a system alert on a screen with no keyboard.
 *
 * **The detail line is not optional decoration.** "Delete this playlist?" is a
 * question about a word; "the videos stay in the library, only the collection
 * goes" is the answer somebody actually needs before pressing it.
 */
export function ConfirmDialog({
  title,
  detail,
  confirmLabel,
  onConfirm,
  onClose,
}: {
  title: string
  detail?: string
  confirmLabel: string
  onConfirm: () => void
  onClose: () => void
}) {
  const { t } = useTranslation()

  return (
    <Dialog label={title} onClose={onClose}>
      <h2 className="text-lg font-medium">{title}</h2>
      {detail && <p className="pt-2 text-sm text-text-2">{detail}</p>}
      <div className="flex justify-end gap-2 pt-4">
        <button
          type="button"
          onClick={onClose}
          className="rounded-lg px-3 py-2 text-sm hover:bg-surface-hover"
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          onClick={() => {
            onConfirm()
            onClose()
          }}
          // The brand's red, which is this app's colour for a destructive
          // confirmation and nothing else — the same one the progress bar uses,
          // and the reason it is never a plain button here.
          className="rounded-lg bg-brand px-3 py-2 text-sm font-medium text-white"
        >
          {confirmLabel}
        </button>
      </div>
    </Dialog>
  )
}
