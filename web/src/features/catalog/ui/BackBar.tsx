import { ArrowLeft } from 'lucide-react'
import { useNavigate } from 'react-router-dom'

/**
 * The top of a screen you drilled into, on a phone.
 *
 * A channel opened from Subscriptions is not a page inside the app's chrome —
 * it has its own subject and its own way out — so the search bar and the tab bar
 * are dropped and this takes their place, the way a phone does everywhere.
 *
 * The title arrives late on purpose. `ChannelHeader` already names the channel
 * in large type, so a bar that named it too would say the same thing twice, an
 * inch apart. It fades in only once that header has scrolled away, which is
 * both what YouTube does and the only moment the name is actually missing.
 */
export function BackBar({
  title,
  showTitle,
  fallback = '/',
}: {
  title: string
  /** Whether the subject has scrolled out of sight and needs naming here. */
  showTitle: boolean
  /** Where to go when there is no history to pop — a link opened cold. */
  fallback?: string
}) {
  const navigate = useNavigate()

  return (
    <header
      className="chrome-blur absolute inset-x-0 top-0 z-40 flex items-center gap-1 px-1"
      style={{ height: 'var(--top-bar)', paddingTop: 'var(--safe-top)' }}
    >
      <button
        type="button"
        aria-label="Back"
        onClick={() => {
          // `navigate(-1)` walks out of the app when this was opened cold —
          // a shared link, or a reload. Same reasoning as the watch layer.
          if (((window.history.state as { idx?: number } | null)?.idx ?? 0) > 0) {
            navigate(-1)
          } else {
            navigate(fallback, { replace: true })
          }
        }}
        className="grid h-11 w-11 shrink-0 place-items-center rounded-full
                   transition-colors duration-150 ease-out hover:bg-surface-hover"
      >
        <ArrowLeft size={22} />
      </button>

      <h1
        className="clamp-1 min-w-0 flex-1 text-base font-medium
                   transition-opacity duration-200 ease-out"
        style={{ opacity: showTitle ? 1 : 0 }}
        // Announced whether or not it is drawn: the heading is the page's
        // subject, and a screen reader should not have to wait for a scroll.
        aria-hidden={undefined}
      >
        {title}
      </h1>
    </header>
  )
}
