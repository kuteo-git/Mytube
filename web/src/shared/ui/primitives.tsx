import clsx from 'clsx'
import { isMissingThumbnail } from '@/shared/ui/thumbnail-placeholder'
import { type ButtonHTMLAttributes, type ReactNode, useState } from 'react'

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
  rounded = 'rounded-3xl',
  children,
}: {
  hue: number
  src?: string
  alt?: string
  rounded?: string
  children?: ReactNode
}) {
  // The stored URL as-is. We deliberately do not rewrite it to maxresdefault
  // here: the ingest picks the widest still available at scan time, so the
  // stored URL is already the best we know exists.
  //
  // A 404 from i.ytimg.com comes with a valid JPEG attached, so the browser
  // decodes it and fires load rather than error — which is why onError alone
  // left a grey YouTube tile on the card instead of the gradient below. onLoad
  // catches that one by its size; see isMissingThumbnail.
  const [failed, setFailed] = useState(false)
  const [loaded, setLoaded] = useState(false)

  const gradient = `linear-gradient(135deg, hsl(${hue} 50% 30%), hsl(${(hue + 45) % 360} 55% 16%))`

  return (
    <div
      style={{ background: gradient }}
      className={clsx('relative aspect-video w-full overflow-hidden', rounded)}
    >
      {/* Shimmer while the image is downloading. Opaque surface with animate-pulse,
          same as VideoCardSkeleton — covers the gradient completely until the
          real image arrives. */}
      {src && !loaded && !failed && (
        <div className="absolute inset-0 animate-pulse bg-surface" />
      )}

      {src && !failed && (
        <img
          src={src}
          alt={alt ?? ''}
          loading="lazy"
          decoding="async"
          className="h-full w-full object-cover"
          onError={() => setFailed(true)}
          onLoad={(e) => {
            const img = e.currentTarget
            if (isMissingThumbnail(img.currentSrc || img.src, img.naturalWidth, img.naturalHeight)) {
              setFailed(true)
            } else {
              setLoaded(true)
            }
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
