import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { createPortal } from 'react-dom'

/**
 * A brief line saying what just happened.
 *
 * For actions whose whole effect is somewhere the page cannot show: a link put
 * on the clipboard changes nothing on screen, so without a word about it the
 * button is indistinguishable from one that does not work. That is not a
 * hypothetical — Share was reported as broken twice for exactly this reason.
 *
 * Deliberately not a notification system. It cannot be dismissed, carries no
 * action and stacks nothing: anything that needs to be acted on or read at
 * leisure belongs on a page, and this project already has one for that
 * (/activity). One message at a time — a second replaces the first, because two
 * lines about two things nobody asked to be told is worse than the newer one
 * alone.
 */

const VISIBLE_MS = 2400

type Toast = { id: number; message: string }

const ToastContext = createContext<((message: string) => void) | null>(null)

/** Show a line. Safe to call where no provider is mounted (tests, /tv later). */
export function useToast(): (message: string) => void {
  return useContext(ToastContext) ?? noop
}

function noop() {}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null)

  // An id per call, so that saying the same thing twice restarts the clock
  // rather than looking like nothing happened.
  const show = useCallback((message: string) => {
    setToast({ id: Date.now() + Math.random(), message })
  }, [])

  useEffect(() => {
    if (!toast) return
    const timer = window.setTimeout(() => setToast(null), VISIBLE_MS)
    return () => window.clearTimeout(timer)
  }, [toast])

  const value = useMemo(() => show, [show])

  return (
    <ToastContext.Provider value={value}>
      {children}
      {toast && <ToastLine key={toast.id} message={toast.message} />}
    </ToastContext.Provider>
  )
}

function ToastLine({ message }: { message: string }) {
  // Portalled to the body rather than rendered in place. The shell's scroller
  // and the player both establish containing blocks and clip their contents
  // (`overflow-hidden` keeps the player's rounded corners) — the same trap the
  // touch settings panel hit, where a list opening upward had its top cut off.
  return createPortal(
    <div
      role="status"
      aria-live="polite"
      className="pointer-events-none fixed inset-x-0 z-[70] flex justify-center px-4"
      // Above the home indicator, and above a tab bar where one is drawn. Read
      // from the same custom property the bars use rather than a number of its
      // own: a toast sitting under the navigation is a toast nobody sees.
      style={{ bottom: 'calc(var(--safe-bottom) + 4.5rem)' }}
    >
      <div className="animate-[toast-in_160ms_ease-out] max-w-full truncate rounded-lg bg-white px-4 py-2.5 text-sm font-medium text-black shadow-lg">
        {message}
      </div>
    </div>,
    document.body,
  )
}
