import { HardDrive, Pin } from 'lucide-react'
import { useStorage, useSetPinned } from '@/features/catalog/application/queries'
import { VideoCard } from '@/features/catalog/ui/VideoCard'
import { formatBytes } from '@/shared/lib/format'

export function StoragePage() {
  const { data, isPending, isError } = useStorage()
  const setPinned = useSetPinned()

  if (isPending) {
    return (
      <div className="mx-auto max-w-4xl px-4 py-6 min-[700px]:px-6">
        <h1 className="text-2xl font-bold">Storage</h1>
        <p className="mt-3 text-sm text-text-2">Loading…</p>
      </div>
    )
  }

  if (isError || !data) {
    return (
      <div className="mx-auto max-w-4xl px-4 py-6 min-[700px]:px-6">
        <h1 className="text-2xl font-bold">Storage</h1>
        <p className="mt-3 text-sm text-text-2">
          Could not load storage usage. Is the gateway running?
        </p>
      </div>
    )
  }

  const ratio = data.budgetBytes > 0 ? data.usedBytes / data.budgetBytes : 0

  return (
    <div className="mx-auto max-w-4xl px-4 py-6 min-[700px]:px-6">
      <h1 className="text-2xl font-bold">Storage</h1>

      <section className="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <StatCard
          icon={<HardDrive size={20} />}
          label="Used"
          value={formatBytes(data.usedBytes)}
          detail={data.budgetBytes > 0 ? `${Math.round(ratio * 100)}% of budget` : undefined}
        />
        <StatCard
          icon={<HardDrive size={20} />}
          label="Budget"
          value={formatBytes(data.budgetBytes)}
          detail="Soft ceiling for autoremoval"
        />
        <StatCard
          icon={<HardDrive size={20} />}
          label="Free on disk"
          value={formatBytes(data.diskFreeBytes)}
        />
        <StatCard label="Videos on disk" value={String(data.videoCount)} />
        <StatCard label="Evicted" value={String(data.evictedCount)} />
      </section>

      {data.evictionCandidates && data.evictionCandidates.length > 0 && (
        <section className="mt-8">
          <h2 className="text-lg font-medium">Next to be removed</h2>
          <p className="mt-1 text-sm text-text-2">
            When storage fills past {formatBytes(data.budgetBytes)}, the least recently watched
            unpinned videos are removed from disk. Their metadata and history are kept.
          </p>
          <div className="mt-4 grid grid-cols-1 gap-x-4 gap-y-10 min-[700px]:grid-cols-2 min-[1000px]:grid-cols-3">
            {data.evictionCandidates.map((video) => (
              <div key={video.id} className="relative">
                <VideoCard video={video} />
                <button
                  type="button"
                  onClick={() =>
                    setPinned.mutate({ videoId: video.id, pinned: !video.pinned })
                  }
                  className="absolute top-2 right-2 grid h-9 w-9 place-items-center rounded-full bg-black/70 text-white hover:bg-black/90"
                  aria-label={video.pinned ? 'Unpin' : 'Keep'}
                >
                  <Pin
                    size={16}
                    className={video.pinned ? 'fill-current' : undefined}
                  />
                </button>
              </div>
            ))}
          </div>
        </section>
      )}

      {data.evictionCandidates && data.evictionCandidates.length === 0 && (
        <section className="mt-8">
          <h2 className="text-lg font-medium">Next to be removed</h2>
          <p className="mt-1 text-sm text-text-2">
            No videos are currently eligible for automatic removal. Every downloaded
            video is either pinned or has been watched recently.
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
