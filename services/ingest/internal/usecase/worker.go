package usecase

import (
	"context"
	"errors"
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

// Worker drains the job queue. It runs in the same process as the RPC server
// for now; moving it out is a matter of starting this loop somewhere else,
// since all coordination happens through the database.
type Worker struct {
	ingest *Ingest
	logger *slog.Logger
}

func NewWorker(ingest *Ingest, logger *slog.Logger) *Worker {
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
		}
	}
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
	result, err := i.downloader.Download(ctx, job.SourceURL, meta.ID, job.PreferredHeight,
		func(p domain.Progress) {
			if err := i.store.Heartbeat(ctx, job.ID, jobLease, p); err != nil {
				w.logger.Warn("heartbeat", "job", job.ID, "error", err)
			}
		})
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
