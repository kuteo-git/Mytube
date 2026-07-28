import clsx from 'clsx'
import type { ButtonHTMLAttributes, ReactNode } from 'react'

/**
 * Shared primitives. Every interactive element here is reachable by keyboard,
 * because Phase 3 drives this UI with a TV remote D-pad.
 */

type IconButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  label: string
  size?: number
}

export function IconButton({ label, size = 40, className, children, ...rest }: IconButtonProps) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      style={{ width: size, height: size }}
      className={clsx(
        'grid shrink-0 place-items-center rounded-full text-text',
        'transition-colors duration-150 ease-out hover:bg-surface-hover',
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  )
}

type PillProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  active?: boolean
  invert?: boolean
}

export function Pill({ active, invert, className, children, ...rest }: PillProps) {
  return (
    <button
      type="button"
      className={clsx(
        'flex h-9 shrink-0 items-center gap-1.5 rounded-full px-4 text-sm font-medium',
        'transition-colors duration-150 ease-out',
        invert || active
          ? 'bg-invert-bg text-invert-text hover:bg-white'
          : 'bg-surface text-text hover:bg-surface-hover',
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  )
}

/**
 * Placeholder avatar. Real channel avatars arrive with ingest; until then a
 * deterministic hue keeps channels visually distinguishable.
 */
export function Avatar({
  hue,
  name,
  size = 36,
}: {
  hue: number
  name: string
  size?: number
}) {
  return (
    <span
      aria-hidden
      style={{
        width: size,
        height: size,
        background: `hsl(${hue} 45% 38%)`,
        fontSize: size * 0.45,
      }}
      className="grid shrink-0 place-items-center rounded-full font-medium text-white select-none"
    >
      {name.charAt(0).toUpperCase()}
    </span>
  )
}

/**
 * Placeholder thumbnail with a fixed 16:9 box so nothing shifts while loading
 * (keeps CLS < 0.1). Swapped for a real <img> once ingest stores thumbnails.
 */
export function ThumbnailSurface({
  hue,
  src,
  alt,
  rounded = 'rounded-xl',
  children,
}: {
  hue: number
  src?: string
  alt?: string
  rounded?: string
  children?: ReactNode
}) {
  return (
    <div
      style={{
        // Shown while the image loads and if it fails, so the card never
        // collapses or flashes white.
        background: `linear-gradient(135deg, hsl(${hue} 50% 30%), hsl(${(hue + 45) % 360} 55% 16%))`,
      }}
      className={clsx('relative aspect-video w-full overflow-hidden', rounded)}
    >
      {src && (
        <img
          src={src}
          alt={alt ?? ''}
          loading="lazy"
          decoding="async"
          className="h-full w-full object-cover"
          onError={(e) => {
            e.currentTarget.style.display = 'none'
          }}
        />
      )}
      {children}
    </div>
  )
}

export function Divider({ className }: { className?: string }) {
  return <hr className={clsx('border-0 border-t border-line', className)} />
}
