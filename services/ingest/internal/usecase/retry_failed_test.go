package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// requeueStore records what the sweep asked for and answers with one job.
type requeueStore struct {
	fakeStore
	asked   [][]time.Duration
	job     domain.Job
	hasJob  bool
	failing bool
}

func (s *requeueStore) RequeueFailed(_ context.Context, backoff []time.Duration) (domain.Job, bool, error) {
	s.asked = append(s.asked, backoff)
	if s.failing {
		return domain.Job{}, false, errors.New("database is having a moment")
	}
	return s.job, s.hasJob, nil
}

func newWorkerWith(store domain.JobStore) *Worker {
	return NewWorker(New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil), nil)
}

// A failed transfer is given another go on its own.
//
// Most of what fails here is not something a person can help with: the
// refusals measured on this address last a couple of minutes and take yt-dlp's
// own downloads with them. Before this, a job that met one waited for somebody
// to notice a red row on /activity and press Retry.
func TestTheSweepAsksForAFailedTransferBack(t *testing.T) {
	store := &requeueStore{
		hasJob: true,
		job:    domain.Job{ID: "j1", SourceURL: "https://youtu.be/abc", Attempts: 1},
	}

	newWorkerWith(store).retryFailed(context.Background())

	if len(store.asked) != 1 {
		t.Fatalf("asked %d times, want 1", len(store.asked))
	}
	// Growing, not flat: by the third try there is no reason to hurry, and
	// every attempt is a request counted against this address.
	waits := store.asked[0]
	if len(waits) != 3 {
		t.Fatalf("offered %d attempts, want 3", len(waits))
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] <= waits[i-1] {
			t.Errorf("wait %d (%s) is not longer than the one before (%s)", i, waits[i], waits[i-1])
		}
	}
}

// Nothing due is the ordinary case and must be silent — this runs every minute
// for as long as the service is up.
func TestTheSweepDoesNothingWhenNothingIsDue(t *testing.T) {
	store := &requeueStore{hasJob: false}
	newWorkerWith(store).retryFailed(context.Background())
	if len(store.asked) != 1 {
		t.Fatalf("asked %d times, want 1", len(store.asked))
	}
}

// The sweep runs beside the queue, so it must not take the queue down with it.
func TestTheSweepSurvivesTheStoreFailing(t *testing.T) {
	store := &requeueStore{failing: true}
	newWorkerWith(store).retryFailed(context.Background())
}
