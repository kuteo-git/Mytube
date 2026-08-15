package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// cooldownStore reports one recent failure and records what gets queued.
type cooldownStore struct {
	fakeStore
	failedAt  time.Time
	hasFailed bool
	enqueued  []domain.Job
	job       domain.Job
}

func (s *cooldownStore) LastFailureFor(context.Context, string) (time.Time, bool, error) {
	return s.failedAt, s.hasFailed, nil
}

func (s *cooldownStore) Enqueue(_ context.Context, j domain.Job) (domain.Job, error) {
	s.enqueued = append(s.enqueued, j)
	return j, nil
}

func (s *cooldownStore) Get(context.Context, string) (domain.Job, error) { return s.job, nil }

func (s *cooldownStore) UnavailableSourceFor(context.Context, string) (domain.UnavailableSource, bool, error) {
	return domain.UnavailableSource{}, false, nil
}

func (s *cooldownStore) ClearUnavailable(context.Context, string) error { return nil }

const cooldownURL = "https://www.youtube.com/watch?v=53KMZ_uRJOc"

// A transfer that just failed is left alone for a while.
//
// A temporary 403 is deliberately not recorded as a refusal — CLAUDE.md is
// emphatic that recording one as permanent would bury hundreds of videos in a
// bad afternoon — so nothing stopped the next request queueing the same doomed
// transfer. The player polls the stream answer every five seconds and every
// poll scheduled a download: three jobs in twenty-six seconds on 53KMZ_uRJOc,
// each dying on the same 403.
func TestSubmitWaitsAfterATransferJustFailed(t *testing.T) {
	store := &cooldownStore{failedAt: time.Now().Add(-10 * time.Second), hasFailed: true}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	if _, err := i.Submit(context.Background(), cooldownURL, "u1", 1080); err == nil {
		t.Fatal("Submit accepted a URL that failed ten seconds ago")
	}
	if len(store.enqueued) != 0 {
		t.Fatalf("queued %d jobs, want 0", len(store.enqueued))
	}
}

// And the wait ends on its own. Nothing here may make a video permanently
// unfetchable — that is what UNAVAILABLE is for, and it is a different answer
// to a different question.
func TestSubmitTriesAgainOnceTheWaitIsOver(t *testing.T) {
	store := &cooldownStore{failedAt: time.Now().Add(-failureCooldown - time.Second), hasFailed: true}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	if _, err := i.Submit(context.Background(), cooldownURL, "u1", 1080); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(store.enqueued) != 1 {
		t.Fatalf("queued %d jobs, want 1", len(store.enqueued))
	}
}

// A person pressing Retry is not the poll coming round again.
//
// The same reasoning that lets Retry clear a permanent refusal: somebody who
// asked for this by hand has said something the machinery cannot know, and a
// wait meant for an automatic retry must not be aimed at them. Without this,
// the button on /activity would do nothing for two minutes and look broken.
func TestRetryIsNotHeldByTheWait(t *testing.T) {
	store := &cooldownStore{
		failedAt:  time.Now().Add(-time.Second),
		hasFailed: true,
		job: domain.Job{
			ID:              "j1",
			SourceURL:       cooldownURL,
			State:           domain.JobFailed,
			PreferredHeight: 1080,
		},
	}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	if _, err := i.RetryJob(context.Background(), "j1", "u1"); err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	if len(store.enqueued) != 1 {
		t.Fatalf("queued %d jobs, want 1", len(store.enqueued))
	}
}
