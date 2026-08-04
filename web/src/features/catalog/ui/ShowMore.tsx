/**
 * The button under a list that has more in it.
 *
 * A button rather than an infinite scroll, because this page is read to answer
 * a question — "did it run", "what did it say" — and a list that grows as you
 * approach the end of it is the wrong shape for that: you cannot tell whether
 * you have reached the bottom or the bottom keeps moving.
 *
 * Says how many are left rather than just "more", so pressing it is a decision
 * rather than a probe.
 */
export function ShowMore({
  remaining,
  onClick,
  busy,
}: {
  remaining: number
  onClick: () => void
  busy?: boolean
}) {
  if (remaining <= 0) return null
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      className="mt-2 h-11 w-full rounded-xl bg-surface text-sm font-medium text-text-2 transition-colors duration-150 ease-out hover:bg-surface-hover hover:text-text disabled:opacity-50"
    >
      {busy ? 'Loading…' : `View more (${remaining})`}
    </button>
  )
}
