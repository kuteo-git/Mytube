import { HardDrive } from 'lucide-react'
import { StorageSettings } from '@/features/settings/ui/StorageSettings'
import { useStorage } from '@/features/catalog/application/queries'
import { VideoCard } from '@/features/catalog/ui/VideoCard'
import { formatBytes } from '@/shared/lib/format'
import { useTranslation } from 'react-i18next'

export function StoragePage() {
  const { t } = useTranslation()
  const { data, isPending, isError } = useStorage()

  if (isPending) {
    return (
      <div className="mx-auto max-w-4xl px-4 py-6 min-[700px]:px-6">
        <h1 className="text-2xl font-bold">{t('nav.storage')}</h1>
        <p className="mt-3 text-sm text-text-2">{t('common.loading')}</p>
      </div>
    )
  }

  if (isError || !data) {
    return (
      <div className="mx-auto max-w-4xl px-4 py-6 min-[700px]:px-6">
        <h1 className="text-2xl font-bold">{t('nav.storage')}</h1>
        <p className="mt-3 text-sm text-text-2">
          {t('empty.couldNotLoad', { what: t('empty.what_storageUsage') })}
        </p>
      </div>
    )
  }

  const ratio = data.budgetBytes > 0 ? data.usedBytes / data.budgetBytes : 0

  return (
    <div className="mx-auto max-w-4xl px-4 py-6 min-[700px]:px-6">
      <h1 className="text-2xl font-bold">{t('nav.storage')}</h1>

      {/* Above the figures, because the figures describe *that folder* under
          *that mode*. Read first, they are numbers without a subject. */}
      <StorageSettings />

      <section className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <StatCard
          icon={<HardDrive size={20} />}
          label={t('ui.used')}
          value={formatBytes(data.usedBytes)}
          detail={
            data.budgetBytes > 0
              ? t('ui.percentOfBudget', { percent: Math.round(ratio * 100) })
              : undefined
          }
        />
        <StatCard
          icon={<HardDrive size={20} />}
          label={t('ui.budget')}
          value={formatBytes(data.budgetBytes)}
          detail={t('pages.storage.softCeiling')}
        />
        <StatCard
          icon={<HardDrive size={20} />}
          label={t('pages.storage.freeOnDisk')}
          value={formatBytes(data.diskFreeBytes)}
        />
        <StatCard label={t('pages.storage.videosOnDisk')} value={String(data.videoCount)} />
        <StatCard label={t('ui.evicted')} value={String(data.evictedCount)} />
        {/* Saving is personal — each member has their own Saved page — but the
            disk is not, and this is the page where somebody asks why it is
            full. A saved video is one the sweep may not delete, whoever saved
            it. */}
        <StatCard
          label={t('ui.kept')}
          value={String(data.keptCount)}
          detail={t('more.savedByAnyone')}
        />
      </section>

      {data.evictionCandidates && data.evictionCandidates.length > 0 && (
        <section className="mt-8">
          <h2 className="text-lg font-medium">{t('pages.storage.nextRemoved')}</h2>
          <p className="mt-1 text-sm text-text-2">
            {t('storagePage.fillsPast', { budget: formatBytes(data.budgetBytes) })}
          </p>
          <div className="mt-4 grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3">
            {data.evictionCandidates.map((video) => (
              <VideoCard key={video.id} video={video} variant="storage" />
            ))}
          </div>
        </section>
      )}

      {data.evictionCandidates && data.evictionCandidates.length === 0 && (
        <section className="mt-8">
          <h2 className="text-lg font-medium">{t('pages.storage.nextRemoved')}</h2>
          <p className="mt-1 text-sm text-text-2">
            {t('empty.noEvictable')}
          </p>
        </section>
      )}
    </div>
  )
}

function StatCard({
  icon,
  label,
  value,
  detail,
}: {
  icon?: React.ReactNode
  label: string
  value: string
  detail?: string
}) {
  return (
    <div className="rounded-xl bg-surface p-4">
      <div className="flex items-center gap-2 text-text-2">
        {icon}
        <span className="text-sm font-medium">{label}</span>
      </div>
      <p className="mt-1 text-xl font-bold tabular-nums">{value}</p>
      {detail && <p className="mt-0.5 text-xs text-text-2">{detail}</p>}
    </div>
  )
}
