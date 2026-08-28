import clsx from 'clsx'
import type { ReactNode } from 'react'

/**
 * A labelled switch, with the label and hint on the left and the switch itself
 * on the right.
 *
 * Extracted rather than copied. This shape existed inline in StorageSettings and
 * again in the player's settings menu, and the proxy screen needed five more —
 * at which point a seventh hand-written switch is seven chances for the track,
 * the knob, the disabled treatment and the `role="switch"` to drift apart.
 *
 * The whole row is the control, not just the switch. A 44px target is the
 * requirement on a phone (the charter's touch rule, and the reason `SettingRow`
 * grew `tall`), and a 24px track does not meet it — but a row does, and it also
 * means the label is not a decoration you can miss by an inch.
 *
 * No hover fill: the switch is its own feedback, and a row that also lights up
 * says the same thing twice.
 */
export function ToggleRow({
  label,
  hint,
  on,
  onChange,
  disabled = false,
  indented = false,
  children,
}: {
  label: string
  hint?: ReactNode
  on: boolean
  onChange: (next: boolean) => void
  /**
   * Drawn dimmed and refusing presses.
   *
   * Used for the switches under a master switch that is off. They keep their
   * stored value rather than reading false — turning the proxy off for an
   * evening should not lose which traffic was going through it.
   */
  disabled?: boolean
  /** Nested under a switch that governs it. */
  indented?: boolean
  /** Anything that belongs under the row — a warning, a measurement. */
  children?: ReactNode
}) {
  return (
    <div className={clsx(indented && 'border-l border-line pl-4')}>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        disabled={disabled}
        onClick={() => onChange(!on)}
        className={clsx(
          'flex w-full items-center justify-between gap-4 py-1.5 text-left',
          // Dimmed and a cursor that says so, rather than a switch that simply
          // does not respond — which reads as broken.
          disabled && 'cursor-not-allowed opacity-40',
        )}
      >
        <span className="min-w-0">
          <span className="block text-sm">{label}</span>
          {hint && <span className="block pt-0.5 text-xs text-text-2">{hint}</span>}
        </span>
        <span
          className={clsx(
            'relative h-6 w-11 shrink-0 rounded-full transition-colors duration-150 ease-out',
            on ? 'bg-brand' : 'bg-surface-hover',
          )}
        >
          <span
            className={clsx(
              'absolute top-1 h-4 w-4 rounded-full bg-white transition-transform duration-150 ease-out',
              on ? 'translate-x-6' : 'translate-x-1',
            )}
          />
        </span>
      </button>
      {children}
    </div>
  )
}
