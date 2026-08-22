import { Fragment, type ReactNode } from 'react'
import { AlertTriangle, CheckCircle, Clock, Loader2, RefreshCw, RotateCcw, X } from 'lucide-react'
import {
  useActivityJobs,
  useCancelJob,
  useClearScans,
  useDismissJob,
  useDismissJobs,
  useRefreshTopics,
  useRetryJob,
  useScanStatus,
  useScans,
} from '@/features/catalog/application/queries'
import { usePagedList } from '@/features/catalog/application/paged-list'
import { ShowMore } from '@/features/catalog/ui/ShowMore'
import type { IngestJob, ScanStatus } from '@/features/catalog/infrastructure/catalogRepository'

import { useToast } from '@/shared/ui/toast'
import { useFormat } from '@/shared/lib/useFormat'
import { useTranslation } from 'react-i18next'

/**
 * What the system has been doing, and what went wrong doing it.
 *
 * Two sources, both already reported by the services: the download queue, whose
 * failures carry the yt-dlp error verbatim, and the scanner, whose failures name
 * the source that could not be read. Together they answer the only two questions
 * worth asking when something is missing — "did it try?" and "what did it say?".
 *
 * Scans are a history rather than a single reading. The scanner used to keep
 * only its last result, in memory, so this page could say how the most recent
 * pass went and nothing else — not even that, across a restart. The question
 * people bring here spans days: a channel has stopped producing new videos, and
 * the first thing worth knowing is whether the scan has been running at all.
 *
 * This is not a log viewer. The four services log to stdout and that stays the
 * place to look for anything underneath the job layer.
 */
export function ActivityPage() {
  const { t } = useTranslation()
  const { data: jobs, isPending: jobsPending } = useActivityJobs()
  const refresh = useRefreshTopics()
  const clearScans = useClearScans()
  const dismissJobs = useDismissJobs()
  const toast = useToast()
  const { data: scanStatus } = useScanStatus()
  const scanning = scanStatus?.running ?? false

  const failed = (jobs ?? []).filter((j) => j.state === 'FAILED')
  const active = (jobs ?? []).filter((j) => j.state === 'RUNNING' || j.state === 'QUEUED')
  const done = (jobs ?? []).filter((j) => j.state === 'SUCCEEDED')

  const handleClearScans = () => {
    clearScans.mutate(undefined, {
      onSuccess: () => toast(t('pages.activity.historyCleared')),
    })
  }

  const handleDismissFailed = () => {
    dismissJobs.mutate('FAILED', {
      onSuccess: (count) => toast(`${count} failed job${count !== 1 ? 's' : ''} cleared`),
    })
  }

  const handleDismissCompleted = () => {
    dismissJobs.mutate('SUCCEEDED', {
      onSuccess: (count) => toast(`${count} completed job${count !== 1 ? 's' : ''} cleared`),
    })
  }

  return (
    <div className="mx-auto max-w-4xl px-4 py-6 min-[700px]:px-6">
      <h1 className="text-2xl font-medium">{t('nav.activity')}</h1>

      <ScanHistory
        refreshing={scanning || refresh.isPending}
        onRefresh={() => refresh.mutate()}
        onClearAll={handleClearScans}
      />

      <section className="mt-8">
        <h2 className="text-lg font-medium">{t('ui.downloads')}</h2>

        {jobsPending && <p className="mt-3 text-sm text-text-2">{t('common.loading')}</p>}

        {!jobsPending && failed.length === 0 && active.length === 0 && done.length === 0 && (
          <p className="mt-3 text-sm text-text-2">
            Nothing has been downloaded yet. Pressing play on a video schedules a copy.
          </p>
        )}

        <JobGroup
          title={t('ui.failed')}
          tone="error"
          jobs={failed}
          render={(job) => <FailedRow job={job} />}
          onClearAll={handleDismissFailed}
          canClear={failed.length > 0}
        />
        <JobGroup
          title={t('pages.activity.inProgress')}
          tone="active"
          jobs={active}
          render={(job) => <ActiveRow job={job} />}
        />
        <JobGroup
          title={t('ui.completed')}
          tone="done"
          jobs={done}
          render={(job) => <DoneRow job={job} />}
          onClearAll={handleDismissCompleted}
          canClear={done.length > 0}
        />
      </section>
    </div>
  )
}

function ScanHistory({
  refreshing,
  onRefresh,
  onClearAll,
}: {
  refreshing: boolean
  onRefresh: () => void
  onClearAll: () => void
}) {
  const { t } = useTranslation()
  const { data, fetchNextPage, hasNextPage, isFetchingNextPage, isPending } = useScans()
  const scans = data?.pages.flatMap((page) => page.scans) ?? []
  const total = data?.pages[0]?.total ?? 0

  return (
    <section className="mt-6">
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-lg font-medium">{t('ui.scans')}</h2>
        <div className="flex items-center gap-2">
          {scans.length > 0 && (
            <button
              type="button"
              onClick={onClearAll}
              className="rounded-full bg-surface px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
            >
              Clear all
            </button>
          )}
          <button
            type="button"
            onClick={onRefresh}
            disabled={refreshing}
            className="flex items-center gap-2 rounded-full bg-surface px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover disabled:opacity-60"
          >
            <RefreshCw size={16} className={refreshing ? 'animate-spin' : undefined} />
            {refreshing ? t('ui.scanning') : t('pages.activity.scanNow')}
          </button>
        </div>
      </div>

      {isPending && <p className="mt-3 text-sm text-text-2">{t('common.loading')}</p>}

      {!isPending && scans.length === 0 && (
        <p className="mt-3 text-sm text-text-2">{t('pages.activity.neverScanned')}</p>
      )}

      {scans.length > 0 && (
        <>
          <ul className="mt-3 space-y-2">
            {scans.map((scan) => (
              <ScanRow key={scan.startedAt} scan={scan} />
            ))}
          </ul>
          <ShowMore
            remaining={hasNextPage ? total - scans.length : 0}
            busy={isFetchingNextPage}
            onClick={() => void fetchNextPage()}
          />
        </>
      )}
    </section>
  )
}

function ScanRow({ scan }: { scan: ScanStatus }) {
  const fmt = useFormat()
  return (
    <li className="rounded-xl bg-surface p-4 text-sm">
      <p className="text-text-2">
        {fmt.relative(scan.startedAt)} · {Math.round(scan.durationMs / 1000)}s ·{' '}
        {scan.sourcesScanned} sources · {scan.videosSeen} videos seen · {scan.videosAdded} added
      </p>
      {scan.sourcesFailed > 0 && (
        <ul className="mt-3 space-y-1.5">
          {scan.errors.map((message) => (
            <li key={message} className="flex gap-2 text-amber-400">
              <AlertTriangle size={16} className="mt-0.5 shrink-0" />
              <span className="break-words">{message}</span>
            </li>
          ))}
        </ul>
      )}
    </li>
  )
}

function FailedRow({ job }: { job: IngestJob }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const retry = useRetryJob()
  return (
    <li className="rounded-xl bg-surface p-4">
      <div className="flex items-start justify-between gap-2">
        <p className="min-w-0 text-sm font-medium break-words">{job.title || job.sourceUrl}</p>
        <div className="flex shrink-0 items-center gap-1">
          {/* Retry first, because it is the action that resolves a failure —
              most of them here are temporary. Without it, hiding would be the
              only thing anybody could do with a failure. */}
          <IconButton
            label={t('pages.activity.retryDownload')}
            onClick={() => retry.mutate(job.id)}
            disabled={retry.isPending}
          >
            <RotateCcw size={16} />
          </IconButton>
          <DismissButton jobId={job.id} />
        </div>
      </div>
      <p className="mt-1 text-xs break-words text-amber-400">{job.errorMessage}</p>
      <p className="mt-1 text-xs text-text-2">{fmt.relative(job.createdAt)}</p>
    </li>
  )
}

function ActiveRow({ job }: { job: IngestJob }) {
  const { t } = useTranslation()
  const cancel = useCancelJob()
  // One video is transferred at a time — a single worker, claiming one job — so
  // everything else in this group is standing in line, not downloading. Both
  // states used to render the same spinner over the same "0%", which is how a
  // system that downloads one video at a time appeared to be downloading two.
  //
  // A spinner is a claim that something is happening and a percentage is a
  // claim about a file being filled; a job that has not started is entitled to
  // neither. It says what it is instead, and keeps its Cancel — leaving the
  // queue is exactly what someone reading this row wants to do.
  const transferring = job.state === 'RUNNING'
  return (
    <li className="rounded-xl bg-surface p-4">
      <div className="flex items-center justify-between gap-2">
        <p className="flex min-w-0 items-center gap-2 text-sm font-medium">
          {transferring ? (
            <Loader2 size={14} className="shrink-0 animate-spin" />
          ) : (
            <Clock size={14} className="shrink-0 text-text-2" />
          )}
          <span className="truncate">{job.title || job.sourceUrl}</span>
        </p>
        {/* Same button, same place, different verb — told apart by its label
            rather than its shape, because a control that moves is a control you
            have to look for. A transfer still running is stopped rather than
            hidden: hiding work that carries on underneath is the one thing this
            must never do. */}
        <IconButton
          label={t('pages.activity.cancelDownload')}
          onClick={() => cancel.mutate(job.id)}
          disabled={cancel.isPending}
        >
          <X size={16} />
        </IconButton>
      </div>
      <p className="mt-1 text-xs text-text-2 tabular-nums">
        {transferring ? `${Math.round(job.progress * 100)}%` : t('pages.activity.queued')}
      </p>
    </li>
  )
}

function DoneRow({ job }: { job: IngestJob }) {
  const fmt = useFormat()
  return (
    <li className="flex items-center gap-2 rounded-xl bg-surface p-4 text-sm">
      <CheckCircle size={14} className="shrink-0 text-text-2" />
      <span className="clamp-1">{job.title || job.sourceUrl}</span>
      <span className="ml-auto shrink-0 text-xs text-text-2">{fmt.relative(job.createdAt)}</span>
      <DismissButton jobId={job.id} />
    </li>
  )
}

function DismissButton({ jobId }: { jobId: string }) {
  const { t } = useTranslation()
  const dismiss = useDismissJob()
  return (
    <IconButton label={t('ui.dismiss')} onClick={() => dismiss.mutate(jobId)} disabled={dismiss.isPending}>
      <X size={16} />
    </IconButton>
  )
}

/**
 * The row actions.
 *
 * 44px of touch target around a 16px icon: these sit at the end of a row a
 * thumb scrolls past, and the charter puts the floor at 44 for exactly this.
 */
function IconButton({
  label,
  onClick,
  disabled,
  children,
}: {
  label: string
  onClick: () => void
  disabled?: boolean
  children: ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={label}
      aria-label={label}
      className="grid h-11 w-11 shrink-0 place-items-center rounded-full text-text-2 transition-colors duration-150 ease-out hover:bg-surface-hover hover:text-text disabled:opacity-50"
    >
      {children}
    </button>
  )
}

function JobGroup({
  title,
  tone,
  jobs,
  render,
  onClearAll,
  canClear,
}: {
  title: string
  tone: 'error' | 'active' | 'done'
  jobs: IngestJob[]
  render: (job: IngestJob) => ReactNode
  onClearAll?: () => void
  canClear?: boolean
}) {
  const { visible, remaining, showMore } = usePagedList(jobs)
  if (jobs.length === 0) return null

  return (
    <div className="mt-4">
      <div className="flex items-center justify-between">
        <h3 className={'text-sm font-medium ' + (tone === 'error' ? 'text-amber-400' : 'text-text-2')}>
          {title}
        </h3>
        {canClear && onClearAll && (
          <button
            type="button"
            onClick={onClearAll}
            className="rounded-full bg-surface px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
          >
            Clear all
          </button>
        )}
      </div>
      {/* Each row renders its own <li> — they differ in shape — so the key goes
          on a keyed Fragment. Wrapping in anything real would make the list's
          children elements containing list items, which is not a list. */}
      <ul className="mt-2 space-y-2">
        {visible.map((job) => (
          <Fragment key={job.id}>{render(job)}</Fragment>
        ))}
      </ul>
      <ShowMore remaining={remaining} onClick={showMore} />
    </div>
  )
}
