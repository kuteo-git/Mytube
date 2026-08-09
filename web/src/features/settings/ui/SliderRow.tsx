import type { ReactNode } from 'react'

/**
 * A slider with its label above it and its value beside the label.
 *
 * Full width rather than sharing a line with the label. It is the only control
 * on this page that is dragged, and at 360px a fixed label and a value readout
 * leave it around 200px — where one pixel is half a percent of volume, which is
 * too coarse to balance a voice against a soundtrack by ear.
 */
export function SliderRow({
  label,
  value,
  min = 0,
  max,
  step = 0.05,
  onChange,
  format,
  hint,
  trailing,
}: {
  label: string
  value: number
  /**
   * Zero for shares of a page, where zero means "none of this". The advanced
   * settings need a real floor: a sampling temperature of zero freezes the feed
   * into one order, and a maximum age of zero empties it.
   */
  min?: number
  max: number
  /** Whole numbers for shares of a feed; fractions for volumes. */
  step?: number
  onChange: (v: number) => void
  format: (v: number) => string
  hint?: string
  /** Shown beside the readout — a badge, a reset, anything the row owns. */
  trailing?: ReactNode
}) {
  return (
    <div>
      <div className="flex items-baseline justify-between gap-3">
        <label htmlFor={`slider-${label}`} className="text-sm text-text-2">
          {label}
        </label>
        <div className="flex items-baseline gap-2">
          {trailing}
          <span className="tabular-nums text-sm font-medium">{format(value)}</span>
        </div>
      </div>
      <input
        id={`slider-${label}`}
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        aria-label={label}
        // Every change, not on release: hearing the balance move while dragging
        // is the entire reason these are on a page you visit with a video
        // playing in the corner.
        onChange={(e) => onChange(Number(e.target.value))}
        className="mt-2 h-6 w-full cursor-pointer accent-brand"
      />
      {hint && <p className="text-xs text-text-2">{hint}</p>}
    </div>
  )
}
