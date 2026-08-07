import type { ReactNode } from 'react'

/**
 * A row of action buttons and status messages at the bottom of a settings
 * section. Standardises the pattern so every section reads the same way.
 */
export function ActionBar({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-3">{children}</div>
  )
}
