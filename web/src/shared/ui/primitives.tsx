import clsx from 'clsx'
import { upgradedThumbnail } from '@/shared/lib/media'
import { useEffect, useState } from 'react'
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
  src,
}: {
  hue: number
  name: string
  size?: number
  /** Channel artwork. The lettered circle stands in when there is none. */
  src?: string
}) {
  // The initial is the fallback, not the design: it was showing for every
  // channel because the picture was never passed in, which made a library of
  // 287 channels look like a wall of identical coloured discs.
  const [failed, setFailed] = useState(false)

  return (
    <span
      aria-hidden
      style={{
        width: size,
        height: size,
        background: `hsl(${hue} 45% 38%)`,
        fontSize: size * 0.45,
      }}
      className="grid shrink-0 place-items-center overflow-hidden rounded-full font-medium text-white select-none"
    >
      {src && !failed ? (
        <img
          src={src}
          alt=""
          width={size}
          height={size}
          loading="lazy"
          decoding="async"
          className="h-full w-full object-cover"
          onError={() => setFailed(true)}
        />
      ) : (
        name.charAt(0).toUpperCase()
      )}
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
  // Try the full-resolution still first, fall back to whatever was stored.
  //
  // Most rows still hold hqdefault at 480×360, from before the ingest learned
  // to pick the largest — and a card is around 560 points wide, twice that on
  // a retina screen, so they arrive visibly soft. Rewriting the URL upgrades
  // every one of them without waiting for the library to be scanned again.
  // maxresdefault does not exist for every video, which is what the fallback
  // is for; only when both fail does the gradient stand alone.
  const upgraded = upgradedThumbnail(src)
  const [stage, setStage] = useState<'upgraded' | 'stored' | 'failed'>(
    upgraded ? 'upgraded' : 'stored',
  )
  useEffect(() => {
    setStage(upgradedThumbnail(src) ? 'upgraded' : 'stored')
  }, [src])

  const chosen = stage === 'upgraded' ? upgraded : stage === 'stored' ? src : undefined

  return (
    <div
      style={{
        // Shown while the image loads and if it fails, so the card never
        // collapses or flashes white.
        background: `linear-gradient(135deg, hsl(${hue} 50% 30%), hsl(${(hue + 45) % 360} 55% 16%))`,
      }}
      className={clsx('relative aspect-video w-full overflow-hidden', rounded)}
    >
      {chosen && (
        <img
          // Keyed on the URL so switching to the fallback actually reloads;
          // React would otherwise reuse the element that had just failed.
          key={chosen}
          src={chosen}
          alt={alt ?? ''}
          loading="lazy"
          decoding="async"
          className="h-full w-full object-cover"
          onError={() => setStage((s) => (s === 'upgraded' ? 'stored' : 'failed'))}
        />
      )}
      {children}
    </div>
  )
}

export function Divider({ className }: { className?: string }) {
  return <hr className={clsx('border-0 border-t border-line', className)} />
}
