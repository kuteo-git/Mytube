# Player & Playback Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the watch page honest and complete — show real download progress, switch to the local file when it lands, show subtitles while still streaming from upstream, open the navigation drawer, and play the next video when one ends.

**Architecture:** Four independent defects, all on the watch path. Three are frontend-only (`Player.tsx`, `AppShell.tsx`, the query layer). One reaches into ingest: the subtitle pass moves ahead of the media transfer and is persisted separately, so captions exist long before the video file does. No proto changes — `SetMediaState` already tolerates an empty media path with a non-empty subtitle set.

**Tech Stack:** Go 1.x (ConnectRPC, `lrstanley/go-ytdlp`), React 19 + TypeScript + TanStack Query v5 + Tailwind v4, Vite.

## Global Constraints

- **All source code, identifiers, comments, commit messages and in-app UI copy MUST be in English.** Vietnamese is allowed only as content data. (CLAUDE.md §4b)
- Frontend is feature-sliced: `domain/` (no React), `application/` (hooks, no HTTP), `infrastructure/` (repository impls), `ui/` (components). **`ui/` never calls `fetch` directly.** (CLAUDE.md §5)
- **No dead controls.** Every rendered element either does something real or is not rendered. (CLAUDE.md §5)
- No component library (no shadcn/ui). Hand-built components against tokens extracted from `Example/*.png`. (CLAUDE.md §5)
- Go services follow `cmd/`, `internal/domain`, `internal/usecase`, `internal/adapter`. `domain` imports no DB/HTTP/framework.
- **Do not merge the subtitle pass into the media download command.** A 429 on the caption endpoint makes yt-dlp exit non-zero and destroys an otherwise-complete video. Two separate passes, always. (CLAUDE.md §8b traps)
- Verification for Go: `go test ./...` and `make check`. Verification for web: `npx tsc --noEmit -p tsconfig.app.json` plus the stated manual browser check. There is no web test runner and this plan does not add one.

---

### Task 1: Poll the job queue while a download is expected

The `useIngestJobs` hook only starts its 2-second poll if a `QUEUED`/`RUNNING` job is *already* in the cached response. The gateway enqueues the download in a goroutine **after** answering `/stream`, so the client's first job fetch sees an empty list, `refetchInterval` returns `false`, and polling never starts. That single fact causes both halves of the reported bug: no percentage, and no switch to the local file without a manual refresh.

**Files:**
- Modify: `web/src/features/catalog/application/queries.ts:139-148`
- Modify: `web/src/features/catalog/application/download.ts:15-30`

**Interfaces:**
- Consumes: `repo.listJobs(activeOnly: boolean): Promise<IngestJob[]>` (already exists in `catalogRepository.ts`)
- Produces:
  - `useIngestJobs(activeOnly?: boolean, forcePoll?: boolean)` — when `forcePoll` is true, polls every 2s regardless of what is currently cached.
  - `useDownloadProgress(videoId: string | undefined, expectDownload: boolean): IngestJob | undefined`

- [ ] **Step 1: Make the poll interval respect a caller-supplied override**

In `web/src/features/catalog/application/queries.ts`, replace the `useIngestJobs` function:

```ts
/**
 * Polls while anything is downloading, then stops.
 *
 * `forcePoll` exists because a caller can know a download is coming before the
 * queue does: pressing play schedules the transfer asynchronously, so the first
 * job list is empty and a self-driving interval would shut off before the job
 * ever appears.
 */
export function useIngestJobs(activeOnly = false, forcePoll = false) {
  return useQuery({
    queryKey: ['ingest-jobs', activeOnly],
    queryFn: () => repo.listJobs(activeOnly),
    refetchInterval: (query) => {
      if (forcePoll) return 2000
      const jobs = query.state.data ?? []
      return jobs.some((j) => j.state === 'QUEUED' || j.state === 'RUNNING') ? 2000 : false
    },
  })
}
```

- [ ] **Step 2: Thread the override through the download-progress hook**

Replace the whole body of `web/src/features/catalog/application/download.ts`:

```ts
import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useIngestJobs } from './queries'
import type { IngestJob } from '../infrastructure/catalogRepository'

/**
 * Tracks the background copy of one video.
 *
 * The gateway answers "how do I play this" with either the local file or an
 * upstream stream, and that answer changes the moment a download finishes. The
 * client cannot know when that happens by watching the video element, so it
 * watches the job instead and re-asks — which is what makes playback move from
 * upstream to disk without the viewer doing anything.
 *
 * `expectDownload` says a transfer is coming even though the queue has not
 * admitted it yet. Playing from upstream always schedules one, and the schedule
 * is a fire-and-forget call made after the stream answer was already sent.
 */
export function useDownloadProgress(
  videoId: string | undefined,
  expectDownload: boolean,
): IngestJob | undefined {
  const { data: jobs } = useIngestJobs(false, expectDownload)
  const queryClient = useQueryClient()

  const job = jobs?.find((j) => j.videoId === videoId)
  const finished = job?.state === 'SUCCEEDED'

  useEffect(() => {
    if (!videoId || !finished) return
    // The copy landed: the stream answer and the media state are both stale.
    void queryClient.invalidateQueries({ queryKey: ['stream', videoId] })
    void queryClient.invalidateQueries({ queryKey: ['video', videoId] })
  }, [videoId, finished, queryClient])

  return job
}
```

- [ ] **Step 3: Update the one call site**

In `web/src/features/watch/ui/Player.tsx:37`, replace:

```tsx
  const download = useDownloadProgress(videoId)
```

with:

```tsx
  // Playing from upstream always schedules a copy, so a job is coming even if
  // the queue has not caught up yet.
  const download = useDownloadProgress(videoId, stream?.source === 'upstream')
```

- [ ] **Step 4: Verify it type-checks**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: no output (success).

- [ ] **Step 5: Verify in the browser**

Run `scripts/dev.sh`. Open a video whose `mediaState` is not `READY`. Open DevTools → Network, filter `ingest/jobs`.
Expected: a request every 2 seconds from the moment the player appears; the badge in the top-left corner reads `Streaming 360p` and grows a percentage within a few seconds.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/catalog/application/queries.ts \
        web/src/features/catalog/application/download.ts \
        web/src/features/watch/ui/Player.tsx
git commit -m "Poll for the download the play button just scheduled"
```

---

### Task 2: Switch to the local file without losing the viewer's place

When the job finishes, Task 1 invalidates `['stream', videoId]` and the query returns a `local` URL. Changing `<video src>` reloads the element, which resets `currentTime` to zero. The element must be told where to resume.

Per the settled spec: switch immediately, restoring position.

**Files:**
- Modify: `web/src/features/watch/ui/Player.tsx:39-56` (add ref), `:230-246` (`onLoadedMetadata`)

**Interfaces:**
- Consumes: `useStream(videoId)` from Task 1's untouched query layer; `stream.url`, `stream.source`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Add a ref that remembers the playback position across a source change**

In `web/src/features/watch/ui/Player.tsx`, after the `videoRef` declaration (line 39), add:

```tsx
  // Swapping the source reloads the element and resets currentTime. The
  // position is captured here just before the swap so playback can resume where
  // it was, rather than restarting the video the viewer was halfway through.
  const resumeAtRef = useRef(initialPositionSeconds)
  const wasPlayingRef = useRef(false)
```

- [ ] **Step 2: Capture position and play state whenever the source is about to change**

Add this effect immediately after the `captionsAvailable` declaration (currently line 58):

```tsx
  // Runs on the render *before* the new src is committed, so the element still
  // holds the old media and its clock is still meaningful.
  useEffect(() => {
    return () => {
      const element = videoRef.current
      if (!element || !Number.isFinite(element.currentTime)) return
      resumeAtRef.current = element.currentTime
      wasPlayingRef.current = !element.paused
    }
  }, [stream?.url])
```

- [ ] **Step 3: Resume from the ref instead of the prop**

In the same file, replace the body of `onLoadedMetadata` (currently lines 230-246) with:

```tsx
          onLoadedMetadata={(e) => {
            const element = e.currentTarget
            if (Number.isFinite(element.duration)) setDuration(element.duration)

            const resumeAt = resumeAtRef.current
            if (resumeAt > 0 && resumeAt < element.duration) {
              element.currentTime = resumeAt
            }

            // Start playing on arrival. If the browser refuses audible
            // autoplay, retry muted rather than leaving a dead frame, and
            // offer the unmute explicitly.
            element.play().catch(() => {
              element.muted = true
              setMuted(true)
              setAutoplayMuted(true)
              void element.play().catch(() => setAutoplayMuted(false))
            })
          }}
```

- [ ] **Step 4: Verify it type-checks**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: no output.

- [ ] **Step 5: Verify in the browser**

Play a video that is not yet on disk. Let it reach roughly 0:30 and wait for the download percentage to reach 100%.
Expected: a brief pause, then playback continues from approximately 0:30 — not from 0:00 — and the `Streaming` badge disappears (the badge only renders for `source === 'upstream'`).

- [ ] **Step 6: Commit**

```bash
git add web/src/features/watch/ui/Player.tsx
git commit -m "Keep the viewer's place when playback moves to the local copy"
```

---

### Task 3: Re-resolve a stream once before declaring it broken

`useStream` caches for 30 minutes with `retry: false`. Upstream URLs are short-lived and bound to the requesting IP, so a stale URL makes `<video>` fire `error` and the player shows "The stream could not be loaded." — while sitting on the dead URL for the rest of the half hour.

**Files:**
- Modify: `web/src/features/catalog/application/queries.ts:128-136`
- Modify: `web/src/features/watch/ui/Player.tsx:48` (state), `:252` (`onError`)

**Interfaces:**
- Consumes: `useQueryClient()` from `@tanstack/react-query`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Shorten the stream cache below the real URL lifetime**

In `web/src/features/catalog/application/queries.ts`, replace `useStream`:

```ts
/**
 * Where to play a video from. Kept out of the video query because the answer
 * changes on its own — an upstream URL expires, and a background download
 * flips the answer to a local file.
 *
 * The cache window is deliberately far shorter than the upstream URL's own
 * lifetime. Serving a URL that died five minutes ago costs a playback failure;
 * re-resolving costs one cheap request.
 */
export function useStream(videoId: string | undefined) {
  return useQuery({
    queryKey: ['stream', videoId],
    queryFn: () => repo.getStream(videoId!),
    enabled: Boolean(videoId),
    staleTime: 5 * 60_000,
    retry: false,
  })
}
```

- [ ] **Step 2: Retry once from the error handler before showing the failure**

In `web/src/features/watch/ui/Player.tsx`, add to the imports at line 2:

```tsx
import { useQueryClient } from '@tanstack/react-query'
```

After the `const download = ...` line, add:

```tsx
  const queryClient = useQueryClient()
  // One re-resolve is allowed per mounted player. An expired upstream URL is
  // the common failure and it fixes itself; anything that survives a fresh URL
  // is a real failure and must be shown rather than retried forever.
  const retriedRef = useRef(false)
```

- [ ] **Step 3: Wire the handler**

Replace line 252, `onError={() => setLoadFailed(true)}`, with:

```tsx
          onError={() => {
            if (retriedRef.current) {
              setLoadFailed(true)
              return
            }
            retriedRef.current = true
            void queryClient.invalidateQueries({ queryKey: ['stream', videoId] })
          }}
```

- [ ] **Step 4: Reset the retry flag when the video changes**

Add this effect next to the others:

```tsx
  useEffect(() => {
    retriedRef.current = false
    setLoadFailed(false)
  }, [videoId])
```

- [ ] **Step 5: Verify it type-checks**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: no output.

- [ ] **Step 6: Verify the retry path in the browser**

Open a video that plays from upstream. In DevTools → Network, right-click the `googlevideo.com` request → Block request URL. Reload the watch page.
Expected: one `GET /api/videos/{id}/stream` request, then a second one after the element errors, and only then the "The stream could not be loaded." message.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/catalog/application/queries.ts web/src/features/watch/ui/Player.tsx
git commit -m "Ask for a fresh stream URL before calling playback broken"
```

---

### Task 4: Open the navigation drawer on the watch page

`AppShell` computes `showFullSidebar = expanded && !isWatch` and `showMiniSidebar = !showFullSidebar && !isWatch`. On `/watch/*` both are false, so the hamburger toggles a state nobody reads. Per the settled spec: an overlay drawer, closed by clicking the scrim or pressing `Escape`, which does not push the player.

**Files:**
- Modify: `web/src/app/AppShell.tsx` (whole file)

**Interfaces:**
- Consumes: `Sidebar({ mini }: { mini: boolean })` from `@/features/navigation/ui/Sidebar` — unchanged.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Replace the shell with one that has a drawer mode**

Replace the entire contents of `web/src/app/AppShell.tsx`:

```tsx
import clsx from 'clsx'
import { useEffect, useState } from 'react'
import { Outlet, useLocation } from 'react-router-dom'
import { Sidebar } from '@/features/navigation/ui/Sidebar'
import { TopBar } from '@/features/navigation/ui/TopBar'

export function AppShell() {
  const { pathname } = useLocation()
  const isWatch = pathname.startsWith('/watch')
  const [expanded, setExpanded] = useState(true)
  const [drawerOpen, setDrawerOpen] = useState(false)

  // youtube.com hides the rail on the watch page to give the player room, and
  // reaches it through a drawer instead. The drawer overlays rather than pushes:
  // narrowing the picture mid-playback to make room for navigation is a worse
  // trade than covering it for the second the viewer is actually navigating.
  const showFullSidebar = expanded && !isWatch
  const showMiniSidebar = !showFullSidebar && !isWatch

  // Leaving the watch page must not leave a drawer hanging over the grid.
  useEffect(() => {
    setDrawerOpen(false)
  }, [pathname])

  useEffect(() => {
    if (!drawerOpen) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setDrawerOpen(false)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [drawerOpen])

  return (
    <div className="min-h-dvh bg-bg">
      <TopBar onToggleSidebar={() => (isWatch ? setDrawerOpen((o) => !o) : setExpanded((e) => !e))} />

      {showFullSidebar && <Sidebar mini={false} />}
      {showMiniSidebar && <Sidebar mini />}

      {isWatch && drawerOpen && (
        <>
          <div
            className="fixed inset-0 top-14 z-30 bg-black/50"
            onClick={() => setDrawerOpen(false)}
            aria-hidden
          />
          <div className="fixed top-14 bottom-0 left-0 z-40">
            <Sidebar mini={false} />
          </div>
        </>
      )}

      <main
        className={clsx(
          'transition-[margin] duration-200 ease-out',
          showFullSidebar && 'ml-60',
          showMiniSidebar && 'ml-[72px]',
        )}
      >
        <Outlet />
      </main>
    </div>
  )
}
```

- [ ] **Step 2: Verify it type-checks**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: no output.

- [ ] **Step 3: Verify in the browser**

Open any watch page. Click the hamburger.
Expected: the rail slides over the content with a dimmed backdrop; the player does not resize. Click the backdrop → closes. Reopen, press `Escape` → closes. Navigate to a video from the drawer → the drawer is gone on arrival.

Then open the home page and click the hamburger.
Expected: unchanged behaviour — the rail collapses to the 72px mini rail and back, with the grid shifting.

- [ ] **Step 4: Commit**

```bash
git add web/src/app/AppShell.tsx
git commit -m "Give the watch page's hamburger something to open"
```

---

### Task 5: Fetch subtitles before the media file, and persist them on arrival

`Downloader.Download` fetches captions *after* the media transfer completes (`downloader.go:315`), and they are only written to the catalog as part of the `READY` transition (`worker.go:139`). A caption file is tens of kilobytes; a 1080p video is hundreds of megabytes. Ordering them this way means captions are unavailable for the entire period they would be most useful — while the viewer is watching the lower-quality upstream stream.

This task splits the subtitle pass out of `Download` and moves it ahead of the transfer. `SetMediaState` already guards `media_path` and `size_bytes` with `CASE WHEN ... <> ''` / `> 0` (`repository.go:375-382`), so it can be called mid-download with subtitles alone and will not clobber anything.

**Files:**
- Modify: `services/ingest/internal/domain/ingest.go:100-106` (the `Downloader` port)
- Modify: `services/ingest/internal/adapter/ytdlp/downloader.go:264-317` (split `Download`)
- Modify: `services/ingest/internal/usecase/worker.go:97-146` (`process`)
- Create: `services/ingest/internal/usecase/worker_test.go`

**Interfaces:**
- Consumes: `domain.Library.SetMediaState(ctx, videoID, state, mediaPath string, sizeBytes int64, subtitles []SubtitleTrack) error` — unchanged signature.
- Produces:
  - `domain.Downloader.FetchSubtitles(ctx context.Context, videoURL, videoID string, height int32) []SubtitleTrack` — never returns an error; a video without captions is a working video.
  - `domain.DownloadResult` loses nothing; its `Subtitles` field is left empty by `Download` from now on.

- [ ] **Step 1: Write the failing test**

Create `services/ingest/internal/usecase/worker_test.go`:

```go
package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// fakeDownloader records the order in which the worker asks for things.
type fakeDownloader struct {
	calls []string
}

func (f *fakeDownloader) Search(context.Context, string, int32) ([]domain.ExternalVideo, error) {
	return nil, nil
}

func (f *fakeDownloader) Preview(context.Context, string) (domain.ExternalVideo, error) {
	f.calls = append(f.calls, "preview")
	return domain.ExternalVideo{ID: "vid1", Title: "Test"}, nil
}

func (f *fakeDownloader) ListPlaylist(context.Context, string, int32) (string, []domain.ExternalVideo, error) {
	return "", nil, nil
}

func (f *fakeDownloader) ResolveStream(context.Context, string) (domain.StreamLocation, error) {
	return domain.StreamLocation{}, nil
}

func (f *fakeDownloader) FetchSubtitles(context.Context, string, string, int32) []domain.SubtitleTrack {
	f.calls = append(f.calls, "subtitles")
	return []domain.SubtitleTrack{{Language: "en", Label: "English", Path: "vid1/1080p.en.vtt"}}
}

func (f *fakeDownloader) Download(context.Context, string, string, int32, func(domain.Progress)) (domain.DownloadResult, error) {
	f.calls = append(f.calls, "download")
	return domain.DownloadResult{MediaPath: "vid1/1080p.mp4", SizeBytes: 1234}, nil
}

// fakeLibrary records every media-state write so the test can assert that
// subtitles were published before the media file existed.
type fakeLibrary struct {
	states    []string
	subtitles [][]domain.SubtitleTrack
}

func (f *fakeLibrary) FindBySourceURL(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeLibrary) UpsertChannel(context.Context, domain.ExternalVideo) error { return nil }
func (f *fakeLibrary) UpsertVideo(context.Context, domain.ExternalVideo, string) error {
	return nil
}
func (f *fakeLibrary) SourceURLFor(context.Context, string) (string, error) { return "", nil }
func (f *fakeLibrary) SetMediaState(_ context.Context, _, state, _ string, _ int64, subs []domain.SubtitleTrack) error {
	f.states = append(f.states, state)
	f.subtitles = append(f.subtitles, subs)
	return nil
}

// fakeStore is a no-op job store; the worker's queue behaviour is not under test.
type fakeStore struct{}

func (fakeStore) Enqueue(_ context.Context, j domain.Job) (domain.Job, error) { return j, nil }
func (fakeStore) Get(context.Context, string) (domain.Job, error)             { return domain.Job{}, nil }
func (fakeStore) List(context.Context, bool, int32) ([]domain.Job, error)     { return nil, nil }
func (fakeStore) Cancel(context.Context, string) error                        { return nil }
func (fakeStore) Claim(context.Context, time.Duration) (domain.Job, error) {
	return domain.Job{}, domain.ErrNotFound
}
func (fakeStore) Heartbeat(context.Context, string, time.Duration, domain.Progress) error {
	return nil
}
func (fakeStore) MarkResolved(context.Context, string, string, string) error { return nil }
func (fakeStore) Finish(context.Context, string, domain.JobState, string) error {
	return nil
}
func (fakeStore) ReleaseExpired(context.Context) (int, error) { return 0, nil }

func TestProcessPublishesSubtitlesBeforeMedia(t *testing.T) {
	downloader := &fakeDownloader{}
	library := &fakeLibrary{}
	ingest := New(downloader, fakeStore{}, library, 1080)
	worker := NewWorker(ingest, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := worker.process(context.Background(), domain.Job{
		ID:              "job1",
		SourceURL:       "https://example.test/watch?v=vid1",
		PreferredHeight: 1080,
	})
	if err != nil {
		t.Fatalf("process: %v", err)
	}

	// Captions must be requested before the transfer, not after it.
	want := []string{"preview", "subtitles", "download"}
	if len(downloader.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", downloader.calls, want)
	}
	for i := range want {
		if downloader.calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", downloader.calls, want)
		}
	}

	// The first media-state write must carry the subtitles while the video is
	// still downloading; that is what makes captions available during upstream
	// playback.
	if len(library.states) != 2 {
		t.Fatalf("media state writes = %v, want 2", library.states)
	}
	if library.states[0] != "DOWNLOADING" {
		t.Errorf("first write state = %q, want DOWNLOADING", library.states[0])
	}
	if len(library.subtitles[0]) != 1 {
		t.Errorf("first write carried %d subtitles, want 1", len(library.subtitles[0]))
	}
	if library.states[1] != "READY" {
		t.Errorf("second write state = %q, want READY", library.states[1])
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./services/ingest/internal/usecase/ -run TestProcessPublishesSubtitlesBeforeMedia -v`
Expected: FAIL to compile — `*fakeDownloader does not implement domain.Downloader (missing method FetchSubtitles)` is not yet true in reverse; the failure will be that `FetchSubtitles` is an extra method and `process` never calls it, so `calls` is `[preview download]`.

- [ ] **Step 3: Add `FetchSubtitles` to the port**

In `services/ingest/internal/domain/ingest.go`, replace the `Downloader` interface:

```go
// Downloader is the port over the external tool. Keeping it an interface is
// what lets the use cases be exercised without touching the network.
type Downloader interface {
	Search(ctx context.Context, query string, limit int32) ([]ExternalVideo, error)
	Preview(ctx context.Context, url string) (ExternalVideo, error)
	ListPlaylist(ctx context.Context, url string, limit int32) (string, []ExternalVideo, error)
	ResolveStream(ctx context.Context, videoURL string) (StreamLocation, error)
	// FetchSubtitles runs ahead of the media transfer so captions are usable
	// while the viewer is still watching the upstream stream. It never returns
	// an error: a video without captions is a working video.
	FetchSubtitles(ctx context.Context, videoURL, videoID string, height int32) []SubtitleTrack
	Download(ctx context.Context, videoURL, videoID string, height int32, onProgress func(Progress)) (DownloadResult, error)
}
```

- [ ] **Step 4: Split the subtitle pass out of `Download` in the adapter**

In `services/ingest/internal/adapter/ytdlp/downloader.go`, add this helper just above `Download` (line 261):

```go
// mediaPaths derives the per-video directory and the media file path. Both the
// media transfer and the subtitle pass need them, and the subtitle filenames
// are derived from the media target, so the two must agree exactly.
func (d *Downloader) mediaPaths(videoID string, height int32) (dir, target string) {
	if height <= 0 {
		height = 1080
	}
	dir = filepath.Join(d.mediaRoot, videoID)
	target = filepath.Join(dir, fmt.Sprintf("%dp.mp4", height))
	return dir, target
}

// FetchSubtitles writes the caption files and reports what landed. It runs
// before the media transfer: captions are tiny and the viewer is watching a
// lower-quality upstream stream in the meantime, which is exactly when they are
// most wanted.
func (d *Downloader) FetchSubtitles(ctx context.Context, videoURL, videoID string, height int32) []domain.SubtitleTrack {
	dir, target := d.mediaPaths(videoID, height)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	return d.fetchSubtitles(ctx, videoURL, dir, videoID, target)
}
```

Then replace the body of `Download` (lines 264-317) with:

```go
func (d *Downloader) Download(ctx context.Context, videoURL, videoID string, height int32, onProgress func(domain.Progress)) (domain.DownloadResult, error) {
	dir, target := d.mediaPaths(videoID, height)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return domain.DownloadResult{}, err
	}
	if height <= 0 {
		height = 1080
	}

	cmd := ytdlp.New().
		Format(fmt.Sprintf("bestvideo[height<=%d]+bestaudio/best[height<=%d]", height, height)).
		MergeOutputFormat("mp4").
		PostProcessorArgs("ffmpeg:-movflags +faststart").
		NoPlaylist().
		NoWarnings().
		NoPart().
		Output(target)

	if onProgress != nil {
		cmd = cmd.ProgressFunc(time.Second, func(update ytdlp.ProgressUpdate) {
			var fraction float32
			if update.TotalBytes > 0 {
				fraction = float32(update.DownloadedBytes) / float32(update.TotalBytes)
			}
			onProgress(domain.Progress{
				Fraction:        fraction,
				DownloadedBytes: int64(update.DownloadedBytes),
				TotalBytes:      int64(update.TotalBytes),
			})
		})
	}

	if _, err := cmd.Run(ctx, videoURL); err != nil {
		return domain.DownloadResult{}, fmt.Errorf("download %q: %w", videoURL, err)
	}

	info, err := os.Stat(target)
	if err != nil {
		return domain.DownloadResult{}, fmt.Errorf("downloaded file missing: %w", err)
	}

	// Captions are fetched by FetchSubtitles, in a separate pass run before this
	// one. Asking for them in the same command means a caption failure — a 429
	// is common — aborts the whole download, losing a video that was otherwise
	// fine. Optional data must not be able to break required data.
	return domain.DownloadResult{
		MediaPath: filepath.Join(videoID, filepath.Base(target)),
		SizeBytes: info.Size(),
	}, nil
}
```

- [ ] **Step 5: Call it from the worker, between metadata and transfer**

In `services/ingest/internal/usecase/worker.go`, insert this block after the `UpsertVideo(..., "DOWNLOADING")` call (currently line 118) and before the comment `// 2. Transfer`:

```go
	// 2. Captions, ahead of the media. They are a few tens of kilobytes against
	// a few hundred megabytes, and they are wanted most during the window when
	// the viewer is watching the lower-quality upstream stream. Failure here is
	// silent by design — a video without captions is still a video.
	if subtitles := i.downloader.FetchSubtitles(ctx, job.SourceURL, meta.ID, job.PreferredHeight); len(subtitles) > 0 {
		if err := i.library.SetMediaState(ctx, meta.ID, "DOWNLOADING", "", 0, subtitles); err != nil {
			w.logger.Warn("publish subtitles", "video", meta.ID, "error", err)
		}
	}
```

Renumber the two following comments: `// 2. Transfer` becomes `// 3. Transfer`, and `// 3. Hand the file over` becomes `// 4. Hand the file over`.

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test ./services/ingest/internal/usecase/ -run TestProcessPublishesSubtitlesBeforeMedia -v`
Expected: PASS.

- [ ] **Step 7: Verify the whole tree still builds**

Run: `make check && go test ./...`
Expected: both succeed.

- [ ] **Step 8: Commit**

```bash
git add services/ingest/internal/domain/ingest.go \
        services/ingest/internal/adapter/ytdlp/downloader.go \
        services/ingest/internal/usecase/worker.go \
        services/ingest/internal/usecase/worker_test.go
git commit -m "Fetch captions before the video file, not after it"
```

---

### Task 6: Show captions while playing from upstream

The backend now publishes subtitles mid-download. The player still refuses to render them: `captionsAvailable` requires `stream?.source === 'local'` (`Player.tsx:58`). It also never re-reads the video record while a download runs, so newly-published tracks would not arrive until a navigation.

**Files:**
- Modify: `web/src/features/watch/ui/Player.tsx:58`
- Modify: `web/src/features/catalog/application/download.ts` (invalidate the video record while running)

**Interfaces:**
- Consumes: `useDownloadProgress(videoId, expectDownload)` from Task 1; `SubtitleTrack[]` passed into `Player` by `WatchPage`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Drop the local-only condition**

In `web/src/features/watch/ui/Player.tsx`, replace line 58:

```tsx
  // Captions no longer wait for the media file: ingest publishes them ahead of
  // the transfer, precisely so they are usable during upstream playback.
  const captionsAvailable = subtitles.length > 0
```

Also update the stale comment on the `captions` state (lines 54-55) to:

```tsx
  // Language code of the active caption track, or null for off. Tracks arrive
  // shortly after playback starts, before the media file finishes downloading.
```

- [ ] **Step 2: Refresh the video record while a download is running**

In `web/src/features/catalog/application/download.ts`, add this effect after the existing one:

```ts
  const running = job?.state === 'RUNNING' || job?.state === 'QUEUED'

  useEffect(() => {
    if (!videoId || !running) return
    // Subtitles are published partway through the transfer, so the video record
    // goes stale while the job is still running. Re-reading it on a slow timer
    // is what makes the caption button appear without a reload.
    const timer = window.setInterval(() => {
      void queryClient.invalidateQueries({ queryKey: ['video', videoId] })
    }, 10_000)
    return () => window.clearInterval(timer)
  }, [videoId, running, queryClient])
```

- [ ] **Step 3: Verify it type-checks**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: no output.

- [ ] **Step 4: Verify in the browser**

Pick a video that is not yet downloaded and is known to have captions (an `@mkbhd` video is a reliable choice — English captions are always present). Press play.
Expected: the `Streaming 360p` badge appears with a percentage; within roughly 10–30 seconds, and **well before** the percentage reaches 100, the `CC` button appears in the control bar. Clicking it shows English subtitles over the upstream stream.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/watch/ui/Player.tsx web/src/features/catalog/application/download.ts
git commit -m "Let captions show before the download finishes"
```

---

### Task 7: Play the next video when one ends

`<video>` has no `onEnded` handler, so a finished video simply stops. Per the settled spec: a 5-second countdown with a cancel button, plus an Autoplay switch whose state survives reloads. The chain stops after 3 consecutive videos with no interaction, because at that point nobody is likely watching — and here every autoplayed video is a fresh multi-hundred-megabyte download against a 34 GiB disk.

**Files:**
- Create: `web/src/features/watch/application/autoplay.ts`
- Modify: `web/src/features/watch/ui/Player.tsx` (props, `onEnded`, countdown overlay, Autoplay toggle in the control bar)
- Modify: `web/src/pages/WatchPage.tsx` (pass the next video, navigate)

**Interfaces:**
- Consumes: `useUpNext(videoId, channelFilter)` from `@/features/catalog/application/queries`, returning `{ videos: Video[] }`.
- Produces:
  - `useAutoplayPreference(): [boolean, (next: boolean) => void]` — persisted under `localStorage["autoplay"]`, defaulting to `true`.
  - `Player` gains two optional props: `nextVideoId?: string` and `onPlayNext?: () => void`.

- [ ] **Step 1: Create the preference hook**

Create `web/src/features/watch/application/autoplay.ts`:

```ts
import { useCallback, useState } from 'react'

const STORAGE_KEY = 'autoplay'

/**
 * Whether finishing a video should start the next one.
 *
 * Persisted because it is a standing preference, not a per-video choice — and
 * because the cost of getting it wrong is asymmetric here: every autoplayed
 * video is a fresh download onto a disk with a hard ceiling, so a viewer who
 * turned this off must find it still off tomorrow.
 */
export function useAutoplayPreference(): [boolean, (next: boolean) => void] {
  const [enabled, setEnabled] = useState(() => {
    return window.localStorage.getItem(STORAGE_KEY) !== 'off'
  })

  const update = useCallback((next: boolean) => {
    window.localStorage.setItem(STORAGE_KEY, next ? 'on' : 'off')
    setEnabled(next)
  }, [])

  return [enabled, update]
}

const CHAIN_KEY = 'autoplay-chain'
const MAX_CHAIN = 3

/**
 * Counts videos played in an unbroken autoplay chain. The chain is what stops
 * an empty room from downloading all night: after three hops with no human
 * input, the next hop does not happen.
 */
export function autoplayChainLength(): number {
  return Number(window.sessionStorage.getItem(CHAIN_KEY) ?? '0')
}

export function recordAutoplayHop(): void {
  window.sessionStorage.setItem(CHAIN_KEY, String(autoplayChainLength() + 1))
}

export function resetAutoplayChain(): void {
  window.sessionStorage.removeItem(CHAIN_KEY)
}

export function autoplayChainExhausted(): boolean {
  return autoplayChainLength() >= MAX_CHAIN
}
```

- [ ] **Step 2: Add the countdown and the toggle to the player**

In `web/src/features/watch/ui/Player.tsx`, extend the props type (lines 21-35) with two optional entries:

```tsx
  nextVideoTitle?: string
  onPlayNext?: () => void
```

Add to the imports on line 1: `SkipForward` from `lucide-react`.

Add these imports near the top:

```tsx
import {
  autoplayChainExhausted,
  resetAutoplayChain,
  useAutoplayPreference,
} from '@/features/watch/application/autoplay'
```

Add state next to the others:

```tsx
  const [autoplayEnabled, setAutoplayEnabled] = useAutoplayPreference()
  // Seconds left before the next video starts, or null when no countdown runs.
  const [countdown, setCountdown] = useState<number | null>(null)
```

Add the countdown driver effect:

```tsx
  useEffect(() => {
    if (countdown === null) return
    if (countdown <= 0) {
      setCountdown(null)
      onPlayNext?.()
      return
    }
    const timer = window.setTimeout(() => setCountdown((n) => (n === null ? null : n - 1)), 1000)
    return () => window.clearTimeout(timer)
  }, [countdown, onPlayNext])
```

Add `onEnded` to the `<video>` element, directly after `onError`:

```tsx
          onEnded={() => {
            setPlaying(false)
            if (!autoplayEnabled || !onPlayNext) return
            // Three hops with nobody touching anything means nobody is here.
            if (autoplayChainExhausted()) return
            setCountdown(5)
          }}
```

Add the overlay, immediately after the `autoplayMuted` block (currently ends line 212):

```tsx
      {countdown !== null && (
        <div className="absolute inset-0 z-20 grid place-items-center bg-black/75 px-6 text-center">
          <div>
            <p className="text-sm text-text-2">Up next in {countdown}</p>
            {nextVideoTitle && <p className="mt-1 clamp-2 text-base font-medium">{nextVideoTitle}</p>}
            <button
              type="button"
              onClick={() => {
                setCountdown(null)
                resetAutoplayChain()
              }}
              className="mt-4 rounded-full bg-surface px-4 py-2 text-sm font-medium transition-colors duration-150 ease-out hover:bg-surface-hover"
            >
              Cancel
            </button>
          </div>
        </div>
      )}
```

Add the toggle to the control bar, immediately before the `captionsAvailable && <CaptionMenu .../>` line:

```tsx
          {onPlayNext && (
            <button
              type="button"
              role="switch"
              aria-checked={autoplayEnabled}
              aria-label="Autoplay"
              title={autoplayEnabled ? 'Autoplay is on' : 'Autoplay is off'}
              onClick={() => setAutoplayEnabled(!autoplayEnabled)}
              className={
                'grid h-9 w-9 place-items-center rounded-full transition-colors duration-150 ease-out hover:bg-white/10 ' +
                (autoplayEnabled ? '' : 'text-text-2')
              }
            >
              <SkipForward size={22} />
            </button>
          )}
```

- [ ] **Step 3: Reset the chain on any real interaction**

Still in `Player.tsx`, add `resetAutoplayChain()` as the first statement inside `toggle`, inside `seekBy`, and inside `applyVolume`. Those three are the only paths a human can reach without navigating, so they are exactly the evidence that somebody is present.

- [ ] **Step 4: Wire the watch page**

Open `web/src/pages/WatchPage.tsx` and read it in full before editing. Pass the first up-next entry into `Player` and navigate on request:

```tsx
  const navigate = useNavigate()
  const next = upNext?.videos?.[0]
```

and on the `<Player .../>` element add:

```tsx
        nextVideoTitle={next?.title}
        onPlayNext={
          next
            ? () => {
                recordAutoplayHop()
                navigate(`/watch/${next.id}`)
              }
            : undefined
        }
```

Import `useNavigate` from `react-router-dom` and `recordAutoplayHop` from `@/features/watch/application/autoplay`.

- [ ] **Step 5: Verify it type-checks**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: no output.

- [ ] **Step 6: Verify in the browser**

Open a short video and seek to a few seconds before the end.
Expected: at the end, a dark overlay reads "Up next in 5" with the next video's title and a Cancel button; at zero it navigates. Press Cancel on a second run → nothing happens, the video stays ended. Click the Autoplay switch off, reload the page, and let a video end → no countdown, and the switch is still off.

Let three videos autoplay in a row without touching anything.
Expected: the fourth does not start.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/watch/application/autoplay.ts \
        web/src/features/watch/ui/Player.tsx \
        web/src/pages/WatchPage.tsx
git commit -m "Play the next video when one ends, unless nobody is watching"
```

---

### Task 8: Correct the stale claim in the charter

`CLAUDE.md` §8b describes the player as it was. Two statements are now wrong: captions are no longer local-only, and the watch page's hamburger is no longer dead.

**Files:**
- Modify: `CLAUDE.md` §8b

- [ ] **Step 1: Update the build-status section**

In `CLAUDE.md`, in the "Phát:" paragraph of §8b, replace the sentence ending `phụ đề (en/vi) với menu CC` with:

```
phụ đề (en/vi) với menu CC — phụ đề được tải **trước** file video nên xem được ngay
trong lúc còn phát upstream. Hết video thì đếm ngược 5 giây rồi phát video kế
(có công tắc Autoplay, tự dừng sau 3 video không ai tương tác).
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "Record what the player actually does now"
```

---

## Verification of the whole plan

Run from the repository root:

```bash
make check
go test ./...
```

Then run `scripts/dev.sh` and walk the four reported defects in order:

1. Open an undownloaded video → percentage climbs, and at 100% playback continues from where it was, in higher quality.
2. Press the hamburger on the watch page → drawer opens over the player, `Escape` closes it.
3. Captions appear before the download completes.
4. Let a video end → countdown, then the next video.
