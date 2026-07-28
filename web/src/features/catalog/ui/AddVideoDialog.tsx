import { Check, Download, Loader2, Search, X } from 'lucide-react'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import {
  useDiscover,
  useIngestJobs,
  useSubmitIngest,
} from '../application/queries'
import { ThumbnailSurface } from '@/shared/ui/primitives'
import { formatBytes, formatDuration, formatViews } from '@/shared/lib/format'
import { hueFromId } from '@/shared/lib/hue'

/**
 * The entry point that replaces youtube.com's "Create" button. Pasting a URL
 * and searching are the same box: a string that looks like a link is submitted
 * directly, anything else is treated as a search.
 */
export function AddVideoDialog({ onClose }: { onClose: () => void }) {
  const [input, setInput] = useState('')
  const [submitted, setSubmitted] = useState('')

  const isUrl = /^https?:\/\//i.test(input.trim())
  const { data: results, isFetching } = useDiscover(isUrl ? '' : submitted)
  const { data: jobs } = useIngestJobs(false)
  const submitIngest = useSubmitIngest()

  const queuedUrls = new Set(
    (jobs ?? [])
      .filter((j) => j.state === 'QUEUED' || j.state === 'RUNNING')
      .map((j) => j.sourceUrl),
  )

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Add video"
      className="fixed inset-0 z-50 grid place-items-start justify-center bg-black/70 p-6 pt-20"
      onClick={onClose}
    >
      <div
        className="flex max-h-[80vh] w-full max-w-3xl flex-col overflow-hidden rounded-xl bg-surface"
        onClick={(e) => e.stopPropagation()}
      >
        <header className="flex items-center gap-3 border-b border-line px-4 py-3">
          <h2 className="flex-1 text-base font-medium">Add video</h2>
          <button
            type="button"
            aria-label="Close"
            onClick={onClose}
            className="grid h-9 w-9 place-items-center rounded-full hover:bg-surface-hover"
          >
            <X size={20} />
          </button>
        </header>

        <form
          className="flex gap-2 p-4"
          onSubmit={(e) => {
            e.preventDefault()
            const value = input.trim()
            if (!value) return
            if (isUrl) submitIngest.mutate(value, { onSuccess: () => setInput('') })
            else setSubmitted(value)
          }}
        >
          <input
            autoFocus
            value={input}
            onChange={(e) => setInput(e.target.value)}
            placeholder="Paste a YouTube link, or search"
            aria-label="Link or search terms"
            className="h-10 flex-1 rounded-full border border-line bg-surface-input px-4 outline-none focus:border-ring"
          />
          <button
            type="submit"
            disabled={submitIngest.isPending}
            className="flex h-10 items-center gap-2 rounded-full bg-invert-bg px-5 text-sm font-medium text-invert-text disabled:opacity-60"
          >
            {isUrl ? <Download size={18} /> : <Search size={18} />}
            {isUrl ? 'Download' : 'Search'}
          </button>
        </form>

        {submitIngest.isError && (
          <p className="px-4 pb-3 text-sm text-brand">Could not queue that link.</p>
        )}

        <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
          {isFetching && (
            <p className="flex items-center gap-2 py-6 text-sm text-text-2">
              <Loader2 size={16} className="animate-spin" /> Searching YouTube…
            </p>
          )}

          <ul className="flex flex-col gap-2">
            {results?.map((video) => {
              const queued = queuedUrls.has(video.sourceUrl)
              return (
                <li key={video.id} className="flex gap-3 rounded-lg p-2 hover:bg-surface-hover">
                  <div className="w-40 shrink-0">
                    <ThumbnailSurface hue={hueFromId(video.id)} rounded="rounded-lg">
                      {video.durationSeconds > 0 && (
                        <span className="absolute right-1 bottom-1 rounded bg-badge px-1 text-[11px] tabular-nums">
                          {formatDuration(video.durationSeconds)}
                        </span>
                      )}
                    </ThumbnailSurface>
                  </div>

                  <div className="min-w-0 flex-1">
                    <p className="clamp-2 text-sm font-medium">{video.title}</p>
                    <p className="mt-1 text-xs text-text-2">
                      {video.channelName}
                      {video.viewCount > 0 && ` • ${formatViews(video.viewCount)}`}
                    </p>
                  </div>

                  {video.inLibrary ? (
                    <Link
                      to={`/watch/${video.id}`}
                      onClick={onClose}
                      className="flex h-9 items-center gap-1.5 self-center rounded-full bg-surface-hover px-3 text-sm font-medium"
                    >
                      <Check size={16} /> In library
                    </Link>
                  ) : (
                    <button
                      type="button"
                      disabled={queued || submitIngest.isPending}
                      onClick={() => submitIngest.mutate(video.sourceUrl)}
                      className="flex h-9 items-center gap-1.5 self-center rounded-full bg-invert-bg px-3 text-sm font-medium text-invert-text disabled:opacity-60"
                    >
                      <Download size={16} /> {queued ? 'Queued' : 'Add'}
                    </button>
                  )}
                </li>
              )
            })}
          </ul>

          {jobs && jobs.length > 0 && (
            <section className="mt-6 border-t border-line pt-4">
              <h3 className="mb-2 text-sm font-medium">Downloads</h3>
              <ul className="flex flex-col gap-2">
                {jobs.slice(0, 8).map((job) => (
                  <li key={job.id} className="text-xs">
                    <div className="flex items-baseline gap-2">
                      <span className="clamp-1 flex-1 text-text">
                        {job.title || job.sourceUrl}
                      </span>
                      <span className="shrink-0 text-text-2">
                        {job.state === 'RUNNING'
                          ? `${Math.round(job.progress * 100)}% of ${formatBytes(job.totalBytes)}`
                          : job.state}
                      </span>
                    </div>
                    {job.state === 'RUNNING' && (
                      <div className="mt-1 h-1 rounded-full bg-line">
                        <div
                          className="h-full rounded-full bg-brand"
                          style={{ width: `${Math.round(job.progress * 100)}%` }}
                        />
                      </div>
                    )}
                    {job.errorMessage && (
                      <p className="mt-1 clamp-2 text-brand">{job.errorMessage}</p>
                    )}
                  </li>
                ))}
              </ul>
            </section>
          )}
        </div>
      </div>
    </div>
  )
}
