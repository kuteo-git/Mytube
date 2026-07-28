# Activity Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the system one place that answers "why did that fail?" — download jobs with their error messages, and the result of the last topic scan.

**Architecture:** Pure frontend plus one gateway route. Every piece of data already exists: `ListJobs` returns `errorMessage` per job, and `GetScanStatus` returns `errors[]` plus counts. Nothing new is computed; the missing layer is `ui/`. This is also the "Notification" entry the charter promised (§5: *Notification + badge → ingest events*), so it doubles as that.

**Tech Stack:** Go (ConnectRPC gateway), React 19 + TypeScript + TanStack Query v5 + Tailwind v4.

## Global Constraints

- **All source code, identifiers, comments and in-app UI copy MUST be in English.** (CLAUDE.md §4b)
- Feature-sliced frontend; `ui/` never calls `fetch`. (CLAUDE.md §5)
- **No dead controls.** (CLAUDE.md §5)
- Deliberately **not** a server log viewer. The four services are four processes; aggregating their `slog` output is a log collector, which is in no phase of this project. `slog` to stdout under `scripts/dev.sh` remains the tool for tracing anything below the job layer.
- Verification: `make check`; web via `npx tsc --noEmit -p tsconfig.app.json` plus stated browser checks.

---

### Task 1: Expose scan status and jobs through the repository layer

`listJobs` already exists. `getScanStatus` does not, though the gateway route does (`router.go:56`).

**Files:**
- Modify: `web/src/features/catalog/infrastructure/catalogRepository.ts`
- Modify: `web/src/features/catalog/application/queries.ts`

**Interfaces:**
- Consumes: `GET /api/topics/scan-status` returning `scanStatusDTO` (`services/gateway/internal/api/ingest.go:130-138`): `{ startedAt, durationMs, sourcesScanned, sourcesFailed, videosSeen, videosAdded, errors }`.
- Produces:
  - `ScanStatus` type exported from `catalogRepository.ts`
  - `repo.getScanStatus(): Promise<ScanStatus>`
  - `useScanStatus()` — polls every 5s while a scan is in flight is **not** required; a plain query with `staleTime: 30_000` is enough because scans are manual or twelve-hourly.

- [ ] **Step 1: Add the type and the fetch**

Read `web/src/features/catalog/infrastructure/catalogRepository.ts` in full first, to match its existing request helper and export style. Then add the type next to the other DTO types:

```ts
export interface ScanStatus {
  startedAt: string
  durationMs: number
  sourcesScanned: number
  sourcesFailed: number
  videosSeen: number
  videosAdded: number
  errors: string[]
}
```

and add to the repository object, following the exact style of the neighbouring methods:

```ts
  getScanStatus: () => request<ScanStatus>('/api/topics/scan-status'),
```

(If the file's helper is named something other than `request`, use whatever the sibling methods use — do not introduce a second convention.)

- [ ] **Step 2: Add the hook**

In `web/src/features/catalog/application/queries.ts`, next to `useIngestJobs`:

```ts
/**
 * Result of the most recent topic scan. Scans are manual or twelve-hourly, so
 * this does not poll; the Activity page refetches it when the user asks.
 */
export function useScanStatus() {
  return useQuery({
    queryKey: ['scan-status'],
    queryFn: () => repo.getScanStatus(),
    staleTime: 30_000,
  })
}
```

- [ ] **Step 3: Verify it type-checks**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add web/src/features/catalog/infrastructure/catalogRepository.ts \
        web/src/features/catalog/application/queries.ts
git commit -m "Read scan status through the repository, like everything else"
```

---

### Task 2: Build the Activity page

**Files:**
- Create: `web/src/pages/ActivityPage.tsx`
- Modify: `web/src/main.tsx` (route)
- Modify: `web/src/features/navigation/ui/Sidebar.tsx` (nav entry)

**Interfaces:**
- Consumes: `useIngestJobs(false)`, `useScanStatus()`, `useRefreshTopics()` — all already exist.
- Produces: route `/activity`.

- [ ] **Step 1: Write the page**

Create `web/src/pages/ActivityPage.tsx`:

```tsx
import { AlertTriangle, CheckCircle, Loader2, RefreshCw } from 'lucide-react'
import {
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
                <p className="flex items-center gap-2 text-sm font-medium">
                  <Loader2 size={14} className="animate-spin" />
                  {job.title || job.sourceUrl}
                </p>
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
  children: React.ReactNode
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
```

- [ ] **Step 2: Register the route**

In `web/src/main.tsx`, add the import and the route inside the `AppShell` element, directly after the `/results` route:

```tsx
import { ActivityPage } from './pages/ActivityPage'
```

```tsx
            <Route path="/activity" element={<ActivityPage />} />
```

- [ ] **Step 3: Add the sidebar entry**

In `web/src/features/navigation/ui/Sidebar.tsx`, add `Activity` to the lucide import and add the entry to `PRIMARY`, after Home:

```ts
  { icon: Activity, label: 'Activity', to: '/activity' },
```

Update the doc comment above `PRIMARY` so it stops claiming there are four fixed entries.

**Note for the implementer:** `/history`, `/saved` and `/storage` in `PRIMARY` are still dead links — no route exists for them. That is a known gap recorded in CLAUDE.md §8b item 4 and is **out of scope for this plan**. Do not silently remove them and do not add stub pages.

- [ ] **Step 4: Verify it type-checks**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: no output.

- [ ] **Step 5: Verify in the browser**

Run `scripts/dev.sh`, open `/activity`.
Expected: the last scan summary with counts, and any failed downloads with their yt-dlp error text. Press "Scan now" → the button spins and the summary updates when it returns.

To confirm the failure path shows something real: stop the network (or add a bogus source to `topics.yaml`, e.g. `https://www.youtube.com/@this-channel-does-not-exist-xyz/videos`), press "Scan now".
Expected: `sourcesFailed` is non-zero and the error names the bogus source. Remove the bogus source afterwards.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/ActivityPage.tsx web/src/main.tsx web/src/features/navigation/ui/Sidebar.tsx
git commit -m "Add an Activity page so failures have somewhere to be seen"
```

---

### Task 3: Update the charter

**Files:**
- Modify: `CLAUDE.md` §8b

- [ ] **Step 1: Record the page**

In `CLAUDE.md` §8b, under "Chưa làm — thứ tự đề xuất khi làm tiếp", remove item 5 ("Nút Refresh trong UI") — the Activity page now carries a working "Scan now" button, so the empty state on Home stops lying. Add to the "Chạy được" section:

```
**Activity:** trang `/activity` gộp hàng đợi tải (kèm lỗi yt-dlp nguyên văn) và kết quả
lần quét gần nhất (kèm nguồn nào hỏng). Có nút "Scan now" thật. Không phải log viewer —
log 4 service vẫn ra stdout.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "Record the Activity page"
```
