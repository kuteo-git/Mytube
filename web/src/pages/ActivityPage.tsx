import type { ReactNode } from 'react'
import { AlertTriangle, CheckCircle, Loader2, RefreshCw, X } from 'lucide-react'
import {
  useCancelJob,
  useIngestJobs,
  useRefreshTopics,
  useScanStatus,
} from '@/features/catalog/application/queries'
import { formatRelative } from '@/shared/lib/format'

/**
 * What the system has been doing, and what went wrong doing it.
 *
 * Two sources, both already reported by the services: the download queue, whose
 * failures carry the yt-dlp error verbatim, and the last topic scan, whose
 * failures name the source that could not be read. Together they answer the
 * only two questions worth asking when something is missing — "did it try?" and
 * "what did it say?".
 *
 * This is not a log viewer. The four services log to stdout and that stays the
 * place to look for anything underneath the job layer.
 */
export function ActivityPage() {
  const { data: jobs, isPending: jobsPending } = useIngestJobs(false)
  const { data: scan } = useScanStatus()
  const refresh = useRefreshTopics()
  const cancelJob = useCancelJob()

  const failed = (jobs ?? []).filter((j) => j.state === 'FAILED')
  const active = (jobs ?? []).filter((j) => j.state === 'RUNNING' || j.state === 'QUEUED')
  const done = (jobs ?? []).filter((j) => j.state === 'SUCCEEDED')

  return (
    <div className="mx-auto max-w-4xl px-6 py-6">
      <h1 className="text-2xl font-medium">Activity</h1>

      <section className="mt-6">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-lg font-medium">Last scan</h2>
          <button
            type="button"
            onClick={() => refresh.mutate()}
            disabled={refresh.isPending}
            className="flex items-center gap-2 rounded-full bg-surface px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover disabled:opacity-60"
          >
            <RefreshCw size={16} className={refresh.isPending ? 'animate-spin' : undefined} />
            {refresh.isPending ? 'Scanning…' : 'Scan now'}
          </button>
        </div>

        {scan ? (
          <div className="mt-3 rounded-xl bg-surface p-4 text-sm">
            <p className="text-text-2">
              {formatRelative(scan.startedAt)} · {Math.round(scan.durationMs / 1000)}s ·{' '}
              {scan.sourcesScanned} sources · {scan.videosSeen} videos seen · {scan.videosAdded}{' '}
              added
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
          </div>
        ) : (
          <p className="mt-3 text-sm text-text-2">No scan has run yet.</p>
        )}
      </section>

      <section className="mt-8">
        <h2 className="text-lg font-medium">Downloads</h2>

        {jobsPending && <p className="mt-3 text-sm text-text-2">Loading…</p>}

        {!jobsPending && failed.length === 0 && active.length === 0 && done.length === 0 && (
          <p className="mt-3 text-sm text-text-2">
            Nothing has been downloaded yet. Pressing play on a video schedules a copy.
          </p>
        )}

        {failed.length > 0 && (
          <JobGroup title="Failed" tone="error">
            {failed.map((job) => (
              <li key={job.id} className="rounded-xl bg-surface p-4">
                <p className="text-sm font-medium">{job.title || job.sourceUrl}</p>
                <p className="mt-1 text-xs break-words text-amber-400">{job.errorMessage}</p>
                <p className="mt-1 text-xs text-text-2">{formatRelative(job.createdAt)}</p>
              </li>
            ))}
          </JobGroup>
        )}

        {active.length > 0 && (
          <JobGroup title="In progress" tone="active">
            {active.map((job) => (
              <li key={job.id} className="rounded-xl bg-surface p-4">
                <div className="flex items-center justify-between gap-2">
                  <p className="flex items-center gap-2 text-sm font-medium min-w-0">
                    <Loader2 size={14} className="animate-spin shrink-0" />
                    <span className="truncate">{job.title || job.sourceUrl}</span>
                  </p>
                  <button
                    type="button"
                    onClick={() => cancelJob.mutate(job.id)}
                    disabled={cancelJob.isPending}
                    className="shrink-0 rounded-full p-1.5 text-text-2 transition-colors duration-150 ease-out hover:bg-surface-hover hover:text-text disabled:opacity-50"
                    aria-label="Cancel download"
                  >
                    <X size={16} />
                  </button>
                </div>
                <p className="mt-1 text-xs text-text-2 tabular-nums">
                  {Math.round(job.progress * 100)}%
                </p>
              </li>
            ))}
          </JobGroup>
        )}

        {done.length > 0 && (
          <JobGroup title="Completed" tone="done">
            {done.slice(0, 20).map((job) => (
              <li key={job.id} className="flex items-center gap-2 rounded-xl bg-surface p-4 text-sm">
                <CheckCircle size={14} className="shrink-0 text-text-2" />
                <span className="clamp-1">{job.title || job.sourceUrl}</span>
                <span className="ml-auto shrink-0 text-xs text-text-2">
                  {formatRelative(job.createdAt)}
                </span>
              </li>
            ))}
          </JobGroup>
        )}
      </section>
    </div>
  )
}

function JobGroup({
  title,
  tone,
  children,
}: {
  title: string
  tone: 'error' | 'active' | 'done'
  children: ReactNode
}) {
  return (
    <div className="mt-4">
      <h3
        className={
          'text-sm font-medium ' + (tone === 'error' ? 'text-amber-400' : 'text-text-2')
        }
      >
        {title}
      </h3>
      <ul className="mt-2 space-y-2">{children}</ul>
    </div>
  )
}
