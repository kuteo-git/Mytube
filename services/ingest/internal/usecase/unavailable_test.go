package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

const membersOnlyError = `preview "https://www.youtube.com/watch?v=6Jw56FSSpg4": exit code 1: exit status 1

ERROR: [youtube] 6Jw56FSSpg4: Join this channel to get access to members-only content like this video, and other exclusive perks.`

// refusalStore remembers refusals in memory, which is all the queue block and
// the reconciliation need to be told apart from the real thing.
type refusalStore struct {
	fakeStore
	recorded []domain.UnavailableSource
	known    map[string]domain.UnavailableSource
	cleared  []string
	reported []string
	enqueued []domain.Job
	job      domain.Job
}

func newRefusalStore() *refusalStore {
	return &refusalStore{known: map[string]domain.UnavailableSource{}}
}

func (s *refusalStore) Get(context.Context, string) (domain.Job, error) { return s.job, nil }

func (s *refusalStore) Enqueue(_ context.Context, j domain.Job) (domain.Job, error) {
	s.enqueued = append(s.enqueued, j)
	return j, nil
}

func (s *refusalStore) MarkUnavailable(_ context.Context, u domain.UnavailableSource) error {
	s.recorded = append(s.recorded, u)
	s.known[u.SourceURL] = u
	return nil
}

func (s *refusalStore) UnavailableSourceFor(_ context.Context, url string) (domain.UnavailableSource, bool, error) {
	u, ok := s.known[url]
	return u, ok, nil
}

func (s *refusalStore) ClearUnavailable(_ context.Context, url string) error {
	s.cleared = append(s.cleared, url)
	delete(s.known, url)
	return nil
}

func (s *refusalStore) UnreportedUnavailable(context.Context, int32) ([]domain.UnavailableSource, error) {
	var out []domain.UnavailableSource
	for _, u := range s.known {
		out = append(out, u)
	}
	return out, nil
}

func (s *refusalStore) MarkUnavailableReported(_ context.Context, url string) error {
	s.reported = append(s.reported, url)
	return nil
}

// refusingDownloader answers every upstream question the way YouTube answers
// for a members-only video.
type refusingDownloader struct {
	fakeDownloader
	previews int
}

func (d *refusingDownloader) Preview(context.Context, string) (domain.ExternalVideo, error) {
	d.previews++
	return domain.ExternalVideo{}, errors.New(membersOnlyError)
}

func (d *refusingDownloader) FetchComments(context.Context, string) ([]domain.YouTubeComment, error) {
	return nil, errors.New(membersOnlyError)
}

const refusedURL = "https://www.youtube.com/watch?v=6Jw56FSSpg4"

// process runs one job through the worker, which is where a refusal met by the
// queue is recorded.
func process(i *Ingest, job domain.Job) error {
	worker := NewWorker(i, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return worker.process(context.Background(), job)
}

// failingDownloader refuses the way a rate limit refuses: for now, not for ever.
type failingDownloader struct {
	fakeDownloader
}

func (d *failingDownloader) Preview(context.Context, string) (domain.ExternalVideo, error) {
	return domain.ExternalVideo{}, errors.New("ERROR: [youtube] abc: HTTP Error 429: Too Many Requests")
}

// The fault as reported: thirteen jobs for one video in two minutes. Every
// route in goes through Submit, which is why the block is there and not on any
// one of the buttons.
func TestSubmitRefusesAKnownUnavailableUrl(t *testing.T) {
	store := newRefusalStore()
	store.known[refusedURL] = domain.UnavailableSource{
		SourceURL: refusedURL,
		Reason:    domain.ReasonMembersOnly,
		Detail:    "ERROR: members-only",
	}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	_, err := i.Submit(context.Background(), refusedURL, "u1", 1080)

	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("Submit error = %v, want ErrUnavailable", err)
	}
	if len(store.enqueued) != 0 {
		t.Fatalf("queued %d jobs for a refused url", len(store.enqueued))
	}
}

func TestSubmitStillQueuesAnythingNotRefused(t *testing.T) {
	store := newRefusalStore()
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	if _, err := i.Submit(context.Background(), refusedURL, "u1", 1080); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(store.enqueued) != 1 {
		t.Fatalf("queued %d jobs, want 1", len(store.enqueued))
	}
}

// Preview is where the id would have been learned, and Preview is what failed —
// so the id comes from the URL, or the catalogue row sits on "queued" for ever.
func TestWorkerRecordsAPermanentRefusalFromPreview(t *testing.T) {
	store := newRefusalStore()
	library := &fakeLibrary{}
	i := New(&refusingDownloader{}, nil, store, library, 1080, nil)

	err := process(i, domain.Job{
		ID:        "job1",
		SourceURL: refusedURL,
	})
	if err == nil {
		t.Fatal("process succeeded against a members-only video")
	}

	if len(store.recorded) != 1 {
		t.Fatalf("recorded %d refusals, want 1", len(store.recorded))
	}
	got := store.recorded[0]
	if got.Reason != domain.ReasonMembersOnly {
		t.Fatalf("reason = %q", got.Reason)
	}
	if got.VideoID != "6Jw56FSSpg4" {
		t.Fatalf("video id = %q, want it read back out of the url", got.VideoID)
	}
	if len(library.states) != 1 || library.states[0] != "UNAVAILABLE" {
		t.Fatalf("catalog states = %v, want one UNAVAILABLE", library.states)
	}
	if len(store.reported) != 1 {
		t.Fatalf("reported %d times, want 1", len(store.reported))
	}
}

// A rate limit must survive this untouched. Recording it would take the video
// out of the library until a person pressed Retry — and a bad afternoon against
// YouTube would do that to hundreds at once.
func TestWorkerLeavesTemporaryFailuresAlone(t *testing.T) {
	store := newRefusalStore()
	i := New(&failingDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	_ = process(i, domain.Job{
		ID:        "job1",
		SourceURL: refusedURL,
	})

	if len(store.recorded) != 0 {
		t.Fatalf("recorded a temporary failure: %+v", store.recorded)
	}
}

// Comments are how this was found: the video had never been downloaded, so
// nothing else had ever asked upstream about it.
func TestFetchCommentsRecordsAndReportsTheRefusal(t *testing.T) {
	store := newRefusalStore()
	library := &fakeLibrary{sourceURL: refusedURL}
	i := New(&refusingDownloader{}, nil, store, library, 1080, nil)

	_, err := i.FetchComments(context.Background(), "6Jw56FSSpg4")

	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("FetchComments error = %v, want ErrUnavailable", err)
	}
	if len(store.recorded) != 1 {
		t.Fatalf("recorded %d refusals, want 1", len(store.recorded))
	}
}

// Once recorded, the question is answered from the record — upstream is not
// asked again, which is the whole point.
func TestFetchCommentsDoesNotAskUpstreamTwice(t *testing.T) {
	store := newRefusalStore()
	downloader := &refusingDownloader{}
	i := New(downloader, nil, store, &fakeLibrary{sourceURL: refusedURL}, 1080, nil)

	_, _ = i.FetchComments(context.Background(), "6Jw56FSSpg4")
	before := len(store.recorded)
	_, err := i.FetchComments(context.Background(), "6Jw56FSSpg4")

	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("second call error = %v", err)
	}
	if len(store.recorded) != before {
		t.Fatal("asked upstream again after it had refused")
	}
}

// A person pressing Retry is the evidence that overturns the judgement.
// Members-only videos do get opened to everyone later.
func TestRetryClearsTheRefusal(t *testing.T) {
	store := newRefusalStore()
	store.job = domain.Job{
		ID:        "job1",
		SourceURL: refusedURL,
		State:     domain.JobFailed,
	}
	store.known[refusedURL] = domain.UnavailableSource{
		SourceURL: refusedURL,
		Reason:    domain.ReasonMembersOnly,
	}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	if _, err := i.RetryJob(context.Background(), "job1", "u1"); err != nil {
		t.Fatalf("RetryJob: %v", err)
	}

	if len(store.cleared) != 1 {
		t.Fatalf("cleared %d refusals, want 1", len(store.cleared))
	}
	if len(store.enqueued) != 1 {
		t.Fatalf("queued %d jobs, want 1 — the block must not survive a retry",
			len(store.enqueued))
	}
}

// The catalogue may be restarting when the refusal is met, and a video left
// looking merely queued is one the feed goes on offering.
func TestReconcileFinishesReportsCatalogNeverReceived(t *testing.T) {
	store := newRefusalStore()
	store.known[refusedURL] = domain.UnavailableSource{
		SourceURL: refusedURL,
		VideoID:   "6Jw56FSSpg4",
		Reason:    domain.ReasonMembersOnly,
	}
	library := &fakeLibrary{}
	i := New(&captionDownloader{}, nil, store, library, 1080, nil)

	i.ReconcileUnavailable(context.Background())

	if len(library.states) != 1 || library.states[0] != "UNAVAILABLE" {
		t.Fatalf("catalog states = %v", library.states)
	}
	if len(store.reported) != 1 {
		t.Fatalf("reported %d times, want 1", len(store.reported))
	}
}
