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

// refusingOnceDownloader answers 403 on the first transfer and succeeds after.
type refusingOnceDownloader struct {
	*fakeDownloader
	attempts int
	at       []time.Time
}

func (d *refusingOnceDownloader) Preview(context.Context, string) (domain.ExternalVideo, error) {
	return domain.ExternalVideo{ID: "vid1", Title: "A three hour mix"}, nil
}

func (d *refusingOnceDownloader) Download(context.Context, string, string, int32, func(domain.Progress)) (domain.DownloadResult, error) {
	d.attempts++
	d.at = append(d.at, time.Now())
	if d.attempts == 1 {
		return domain.DownloadResult{}, errors.New("ERROR: unable to download video data: HTTP Error 403: Forbidden")
	}
	return domain.DownloadResult{MediaPath: "vid1/1080p.mp4", SizeBytes: 1234}, nil
}

func refusedWorker(t *testing.T, d *refusingOnceDownloader, delay time.Duration) *Worker {
	t.Helper()
	ingest := New(d, nil, fakeStore{}, &fakeLibrary{}, 1080, nil)
	w := NewWorker(ingest, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w.resolveDelay = delay
	return w
}

// The second attempt has to stand somewhere else in time than the first.
//
// Measured on g55XEx2oFaE, a three-hour mix: claimed at 18:06:17, refused at
// :21, resolved again immediately and refused again at :23. Two attempts inside
// six seconds, both inside the same refusal — and minutes later the same URL
// shape answered 206 to a bounded range and 200 to no range at all, first ask.
// Nothing was wrong with the video or the request; the retry was simply
// standing in the same place as the thing it was retrying.
func TestTheSecondAttemptWaitsBeforeResolvingAgain(t *testing.T) {
	d := &refusingOnceDownloader{fakeDownloader: &fakeDownloader{}}
	worker := refusedWorker(t, d, 40*time.Millisecond)

	err := worker.process(context.Background(), domain.Job{ID: "job1", SourceURL: "https://example.test/v"})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if d.attempts != 2 {
		t.Fatalf("attempts = %d, want 2", d.attempts)
	}
	if gap := d.at[1].Sub(d.at[0]); gap < 40*time.Millisecond {
		t.Errorf("second attempt came %v after the first, want at least the backoff", gap)
	}
}

// Cancelling during the wait must not start the transfer it was waiting for.
//
// The pause happens with a job the viewer may have given up on, and a worker
// that ignored that would spend the slot on a video nobody is waiting for —
// the same fault the heartbeat exists to prevent one level down.
func TestCancellingDuringTheWaitStopsTheSecondAttempt(t *testing.T) {
	d := &refusingOnceDownloader{fakeDownloader: &fakeDownloader{}}
	worker := refusedWorker(t, d, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_ = worker.process(ctx, domain.Job{ID: "job1", SourceURL: "https://example.test/v"})

	if d.attempts != 1 {
		t.Errorf("attempts = %d, want the second one abandoned", d.attempts)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("waited %v for a cancelled job", elapsed)
	}
}
