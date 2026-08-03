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
  max,
  onChange,
  format,
  hint,
}: {
  label: string
  value: number
  max: number
  onChange: (v: number) => void
  format: (v: number) => string
  hint?: string
}) {
  return (
    <div>
      <div className="flex items-baseline justify-between gap-3">
        <label htmlFor={`slider-${label}`} className="text-sm text-text-2">
          {label}
        </label>
        <span className="tabular-nums text-sm font-medium">{format(value)}</span>
      </div>
      <input
        id={`slider-${label}`}
        type="range"
        min={0}
        max={max}
        step={0.05}
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
