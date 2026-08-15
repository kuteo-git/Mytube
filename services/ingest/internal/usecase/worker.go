package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

const (
	// The lease must comfortably exceed the heartbeat interval, or a slow
	// download would keep requeueing itself.
	jobLease      = 2 * time.Minute
	pollInterval  = 3 * time.Second
	sweepInterval = time.Minute
)

// errJobCancelled says the transfer stopped because someone asked it to, which
// is not a failure and must not be recorded as one.
var errJobCancelled = errors.New("job cancelled while running")

// Worker drains the job queue. It runs in the same process as the RPC server
// for now; moving it out is a matter of starting this loop somewhere else,
// since all coordination happens through the database.
type Worker struct {
	ingest *Ingest
	logger *slog.Logger
}

func NewWorker(ingest *Ingest, logger *slog.Logger) *Worker {
	// Guarded the same way New guards it. Every path through this type logs,
	// so a nil logger is not a quiet worker, it is a panic on the first thing
	// that happens to go wrong.
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Worker{ingest: ingest, logger: logger}
}

func (w *Worker) Run(ctx context.Context) {
	go w.sweep(ctx)

	for {
		claimed, err := w.runOnce(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			w.logger.Error("job failed", "error", err)
		}

		if !claimed {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
			}
		}
	}
}

// sweep returns jobs abandoned by a crashed worker to the queue.
func (w *Worker) sweep(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			released, err := w.ingest.store.ReleaseExpired(ctx)
			if err != nil {
				w.logger.Warn("release expired jobs", "error", err)
				continue
			}
			if released > 0 {
				w.logger.Info("released expired jobs", "count", released)
			}

			w.retryFailed(ctx)
		}
	}
}

// How long a failed transfer waits before being tried again, per attempt.
//
// Growing, because the failure this is written for is upstream refusing this
// address for a few minutes at a time — measured repeatedly, and it takes
// yt-dlp's own downloads with it — and by the third try there is no reason to
// be in a hurry. Every attempt is also a request counted against this address
// (CLAUDE.md §8, risk 6), so being patient is not merely polite.
//
// Three of them, ending after about forty minutes. Past that, waiting longer
// stops being a retry and becomes a promise nobody is watching for; the job
// stays FAILED with the Retry button it always had.
var retryBackoff = []time.Duration{2 * time.Minute, 10 * time.Minute, 30 * time.Minute}

// retryFailed gives one failed transfer another go.
//
// A failed job used to wait for a person, and most of what fails here is not
// something a person can help with: a 403 that lasts two minutes needs nothing
// but two minutes. yt-dlp already recovers this way within a single transfer —
// "transfer refused, resolving again" and then a job that succeeds — and this
// is the same idea one level up, for the refusals that outlast a process.
//
// What it is deliberately not: a second opinion about whether a video can be
// fetched. Anything upstream has refused for good is recorded in
// unavailable_sources and skipped here, because that question already has one
// place that answers it.
func (w *Worker) retryFailed(ctx context.Context) {
	job, requeued, err := w.ingest.store.RequeueFailed(ctx, retryBackoff)
	if err != nil {
		w.logger.Warn("requeue failed jobs", "error", err)
		return
	}
	if !requeued {
		return
	}
	w.logger.Info("failed transfer queued again",
		"job", job.ID, "url", job.SourceURL, "attempt", job.Attempts+1)
}

func (w *Worker) runOnce(ctx context.Context) (bool, error) {
	job, err := w.ingest.store.Claim(ctx, jobLease)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	w.logger.Info("job claimed", "job", job.ID, "url", job.SourceURL)

	if err := w.process(ctx, job); err != nil {
		// The row already says CANCELLED — that is how the transfer was told to
		// stop. Writing FAILED over it would turn a deliberate departure into an
		// error, and put a Retry button under it.
		if errors.Is(err, errJobCancelled) {
			return true, nil
		}
		if finishErr := w.ingest.store.Finish(ctx, job.ID, domain.JobFailed, err.Error()); finishErr != nil {
			w.logger.Error("mark job failed", "job", job.ID, "error", finishErr)
		}
		return true, err
	}

	return true, w.ingest.store.Finish(ctx, job.ID, domain.JobSucceeded, "")
}

func (w *Worker) process(ctx context.Context, job domain.Job) error {
	i := w.ingest

	// 1. Metadata first. The video becomes visible in the library immediately,
	// marked as downloading, instead of appearing only once bytes have landed.
	meta, err := i.downloader.Preview(ctx, job.SourceURL)
	if err != nil {
		// A refusal that will not change. Recorded before the job is failed, so
		// nothing queues this URL again, and the catalogue is told so the video
		// can say why rather than sitting on "queued" for ever.
		//
		// The id comes from the URL: Preview is where it would have been
		// learned, and it is precisely Preview that failed. Everything in this
		// system builds the URL from the id, so reading it back is that
		// construction reversed rather than a guess.
		i.recordUnavailable(ctx, job.SourceURL, videoIDFromURL(job.SourceURL), err)
		return err
	}
	if meta.ID == "" {
		return errors.New("upstream returned no video id")
	}

	// A broadcast still running has no end to download to. yt-dlp follows it for
	// as long as it lasts, which held the one worker slot for hours and left
	// every later job queued at 0% — and the file it is building is not the
	// video anyone asked for anyway, but however much of it happened to be on
	// while they were away. Cancelling reaches the process now, but a transfer
	// that cannot finish should not be started.
	if meta.IsLive {
		return fmt.Errorf("%w: %s is broadcasting live", domain.ErrInvalid, meta.ID)
	}

	if err := i.store.MarkResolved(ctx, job.ID, meta.ID, meta.Title); err != nil {
		return err
	}
	if err := i.library.UpsertChannel(ctx, meta); err != nil {
		return err
	}
	// Preview above already fetched full metadata, so the category is free
	// here — the same source of truth for topics the rest of the system uses.
	meta.Topics = categoryTopics(meta)

	if err := i.library.UpsertVideo(ctx, meta, "DOWNLOADING"); err != nil {
		return err
	}

	// 2. Captions, ahead of the media. They are a few tens of kilobytes against
	// a few hundred megabytes, and they are wanted most during the window when
	// the viewer is watching the lower-quality upstream stream. Failure here is
	// silent by design — a video without captions is still a video.
	//
	// Behind the same claim as the play-time fetch: pressing play starts one of
	// these already, and two fetches racing publish two different lists over
	// each other. See fetchSubtitlesOnce.
	i.fetchSubtitlesOnce(ctx, job.SourceURL, meta.ID, job.PreferredHeight)

	// 3. Transfer, heartbeating so the lease stays alive and the UI can show a
	// real progress bar rather than a spinner.
	//
	// A heartbeat that finds the job no longer running means it was cancelled,
	// and cancelling has to reach the process: marking the row alone left yt-dlp
	// pulling a video nobody was waiting for, and for a livestream that never
	// ends, so the single worker slot stayed occupied and every later job sat
	// queued at 0%.
	transfer, stopTransfer := context.WithCancel(ctx)
	defer stopTransfer()

	var cancelled bool
	onProgress := func(p domain.Progress) {
		switch err := i.store.Heartbeat(transfer, job.ID, jobLease, p); {
		case errors.Is(err, domain.ErrJobNotRunning):
			cancelled = true
			w.logger.Info("transfer cancelled, stopping", "job", job.ID, "video", meta.ID)
			stopTransfer()
		case err != nil:
			w.logger.Warn("heartbeat", "job", job.ID, "error", err)
		}
	}

	result, err := i.downloader.Download(transfer, job.SourceURL, meta.ID, job.PreferredHeight, onProgress)

	// A refused transfer is worth one more process, because the refusal usually
	// belongs to the URL rather than to the video.
	//
	// YouTube sometimes signs a URL that is dead on arrival: every request to it
	// answers 403 for as long as it lives — 20 of 20, measured on the instant
	// tier. yt-dlp resolves once when it starts, so --retries throws the same
	// dead URL at the same host and fails the same way, which is why three
	// attempts on `cT_ZlNvkW60` each died in three seconds without reaching a
	// byte. A new process resolves again, and the fourth carried 70MB.
	//
	// Until this, the queue called that permanent, and the video arrived only
	// because somebody pressed play four times — the shape that once turned one
	// video into thirteen jobs in two minutes.
	//
	// Once, not twice: a second refusal is upstream's answer rather than a bad
	// URL. And never for a permanent refusal — asking a members-only video again
	// is exactly what must not happen.
	if err != nil && !cancelled && ctx.Err() == nil {
		if _, permanent := domain.ReasonOf(domain.AsUnavailable(err)); !permanent {
			w.logger.Warn("transfer refused, resolving again",
				"job", job.ID, "video", meta.ID, "error", err)
			result, err = i.downloader.Download(transfer, job.SourceURL, meta.ID, job.PreferredHeight, onProgress)
		}
	}

	if cancelled {
		// Not a failure: it was asked for. EVICTED is what the catalogue calls a
		// video with no copy that can be fetched again — the UI already says
		// "Removed — press to fetch again" — and FAILED would offer a retry for
		// something that never went wrong.
		if stateErr := i.library.SetMediaState(ctx, meta.ID, "EVICTED", "", 0, nil); stateErr != nil {
			w.logger.Error("mark media evicted after cancel", "video", meta.ID, "error", stateErr)
		}
		return errJobCancelled
	}
	if err != nil {
		// Leave the catalog row in a state the UI can explain, rather than
		// stuck on "downloading" forever.
		if stateErr := i.library.SetMediaState(ctx, meta.ID, "FAILED", "", 0, nil); stateErr != nil {
			w.logger.Error("mark media failed", "video", meta.ID, "error", stateErr)
		}
		return err
	}

	// 4. Hand the file over. From here playback comes from disk and upstream is
	// never touched again for this video.
	if err := i.library.SetMediaState(ctx, meta.ID, "READY", result.MediaPath, result.SizeBytes, result.Subtitles); err != nil {
		return err
	}

	w.logger.Info("job succeeded", "job", job.ID, "video", meta.ID,
		"bytes", result.SizeBytes, "subtitles", len(result.Subtitles))
	return nil
}
