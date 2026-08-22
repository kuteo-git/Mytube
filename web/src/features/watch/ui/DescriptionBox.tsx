import clsx from 'clsx'
import { useState } from 'react'
import type { Video } from '@/features/catalog/domain/video'
import { useFormat } from '@/shared/lib/useFormat'
import { useTranslation } from 'react-i18next'


/** Collapsible description. Also surfaces local-only facts: disk size and media state. */
export function DescriptionBox({ video }: { video: Video }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const [expanded, setExpanded] = useState(false)

  return (
    <section
      className={clsx(
        'rounded-xl bg-surface p-3 text-sm',
        !expanded && 'cursor-pointer hover:bg-surface-hover',
      )}
      onClick={() => !expanded && setExpanded(true)}
    >
      <p className="font-medium">
        {[
          video.viewCount > 0 ? fmt.views(video.viewCount) : null,
          video.publishedAt ? fmt.date(video.publishedAt) : null,
        ]
          .filter(Boolean)
          .join(' • ')}{' '}
        {video.hashtags.map((tag) => (
          <span key={tag} className="text-link">
            {tag}{' '}
          </span>
        ))}
      </p>

      <p className={clsx('mt-2 whitespace-pre-line', !expanded && 'clamp-2')}>
        {video.description}
      </p>

      {expanded && (
        <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 border-t border-line-subtle pt-3 text-text-2">
          <dt>{t('description.onDisk')}</dt>
          <dd className="text-text">{fmt.bytes(video.sizeBytes)}</dd>
          <dt>{t('description.addedToLibrary')}</dt>
          <dd className="text-text">{fmt.date(video.addedAt)}</dd>
          <dt>{t('description.mediaState')}</dt>
          <dd className="text-text">{video.mediaState}</dd>
        </dl>
      )}

      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation()
          setExpanded((v) => !v)
        }}
        className="mt-2 font-medium"
      >
        {expanded ? t('description.showLess') : '…more'}
      </button>
    </section>
  )
}
