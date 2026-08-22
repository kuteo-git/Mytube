import clsx from 'clsx'
import type { ReactNode } from 'react'

/** A card with a heading, matching the stat cards on the Storage page. */
/**
 * @param headless drops the icon and the heading, for a screen that is only
 * about this one section — the phone's Settings opens each on its own, and the
 * back bar already carries the name. Two headings an inch apart saying the same
 * word is the same fault the channel page's title fade exists to avoid.
 * The description stays: it says what the controls are for, which the title
 * does not.
 */
export function SettingsSection({
  icon,
  title,
  description,
  headless = false,
  children,
}: {
  icon: ReactNode
  title: string
  description?: string
  headless?: boolean
  children: ReactNode
}) {
  return (
    <section className="mt-10 rounded-xl bg-surface p-4 ring-1 ring-transparent transition-shadow duration-150 ease-out hover:ring-line min-[700px]:p-5">
      {!headless && (
        <div className="flex items-center gap-3">
          <span className="grid h-9 w-9 shrink-0 place-items-center rounded-full bg-surface-hover text-text-2">
            {icon}
          </span>
          <h2 className="text-base font-medium">{title}</h2>
        </div>
      )}
      {description && (
        <p className={clsx('text-sm leading-relaxed text-text-2', !headless && 'mt-1')}>
          {description}
        </p>
      )}
      <div className="mt-4 flex flex-col gap-4">{children}</div>
    </section>
  )
}

/**
 * Label beside its control.
 *
 * Used for the controls that fit on one line. Sliders get their own row type,
 * because they are the one thing here that is dragged rather than tapped and
 * the width a fixed label leaves them on a phone makes them coarse.
 */
export function SettingRow({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div>
      <div className="flex items-center gap-3">
        <label className="w-24 shrink-0 text-sm text-text-2 min-[700px]:w-28">
          {label}
        </label>
        <div className="flex min-w-0 flex-1 items-center gap-2">{children}</div>
      </div>
      {hint && <p className="mt-1 pl-24 text-xs text-text-2 min-[700px]:pl-28">{hint}</p>}
    </div>
  )
}
