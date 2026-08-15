package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// liveDownloader previews a broadcast that is still running.
type liveDownloader struct{ *fakeDownloader }

func (d liveDownloader) Preview(context.Context, string) (domain.ExternalVideo, error) {
	d.calls = append(d.calls, "preview")
	return domain.ExternalVideo{ID: "vid1", Title: "Live now", IsLive: true}, nil
}

// A broadcast still running has no end to download to. yt-dlp follows it for as
// long as it lasts, which held the single worker slot for hours and left every
// later job queued at 0% — measured on a live stream left running after the
// viewer had walked away.
func TestALiveBroadcastIsNotDownloaded(t *testing.T) {
	downloader := &fakeDownloader{}
	library := &fakeLibrary{}
	ingest := New(liveDownloader{downloader}, nil, fakeStore{}, library, 1080, nil)
	worker := NewWorker(ingest, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := worker.process(context.Background(), domain.Job{ID: "job1", SourceURL: "https://example.test/live"})

	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("err = %v, want it to wrap ErrInvalid", err)
	}
	for _, call := range downloader.calls {
		if call == "download" {
			t.Fatalf("a transfer was started for a live broadcast: %v", downloader.calls)
		}
	}
	// Nothing was promised, so nothing should be left claiming to be downloading.
	if len(library.states) != 0 {
		t.Fatalf("media state writes = %v, want none", library.states)
	}
}

// cancellingStore reports on the first heartbeat that the job is no longer
// running, which is what a viewer leaving the watch page looks like from here.
type cancellingStore struct{ fakeStore }

func (cancellingStore) Heartbeat(context.Context, string, time.Duration, domain.Progress) error {
	return domain.ErrJobNotRunning
}

// watchingDownloader reports progress once and then waits to be stopped,
// standing in for yt-dlp: it ends only when its context is cancelled.
type watchingDownloader struct {
	*fakeDownloader
	stopped bool
}

func (d *watchingDownloader) Download(
	ctx context.Context, _, _ string, _ int32, progress func(domain.Progress),
) (domain.DownloadResult, error) {
	d.calls = append(d.calls, "download")
	progress(domain.Progress{Fraction: 0.1})

	select {
	case <-ctx.Done():
		d.stopped = true
		return domain.DownloadResult{}, ctx.Err()
	case <-time.After(2 * time.Second):
		return domain.DownloadResult{MediaPath: "vid1/1080p.mp4"}, nil
	}
}

// Cancelling used to mark the job row and nothing else. The worker runs in its
// own process, so yt-dlp carried on downloading a video nobody was waiting for
// — and for a livestream it never stopped at all, holding the single worker
// slot while every later job sat queued at 0%. The heartbeat is the only moment
// a transfer in flight listens.
func TestCancellingAJobStopsTheTransfer(t *testing.T) {
	downloader := &watchingDownloader{fakeDownloader: &fakeDownloader{}}
	library := &fakeLibrary{}
	ingest := New(downloader, nil, cancellingStore{}, library, 1080, nil)
	worker := NewWorker(ingest, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := worker.process(context.Background(), domain.Job{ID: "job1", SourceURL: "https://example.test/v"})

	if !errors.Is(err, errJobCancelled) {
		t.Fatalf("err = %v, want errJobCancelled", err)
	}
	if !downloader.stopped {
		t.Fatal("the transfer was not stopped — cancelling did not reach the process")
	}
	// Cancelled is not broken. FAILED would put a Retry button under something
	// that never went wrong; EVICTED is the catalogue's word for "no copy, and
	// you may fetch it again".
	if len(library.states) == 0 || library.states[len(library.states)-1] != "EVICTED" {
		t.Fatalf("media states = %v, want the last to be EVICTED", library.states)
	}
}
