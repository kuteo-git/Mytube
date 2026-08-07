package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// recordingStore is a job store that remembers what it was asked to do, so a
// test can check the sequence rather than the wording of a query.
type recordingStore struct {
	fakeStore
	job       domain.Job
	getErr    error
	enqueued  []domain.Job
	dismissed []string
	dismissEr error
}

func (s *recordingStore) Get(context.Context, string) (domain.Job, error) {
	return s.job, s.getErr
}

func (s *recordingStore) Enqueue(_ context.Context, j domain.Job) (domain.Job, error) {
	s.enqueued = append(s.enqueued, j)
	return j, nil
}

func (s *recordingStore) Dismiss(_ context.Context, jobID string) error {
	s.dismissed = append(s.dismissed, jobID)
	return s.dismissEr
}

func (s *recordingStore) DismissByState(_ context.Context, state string) (int64, error) { return 0, nil }

func failedJob() domain.Job {
	return domain.Job{
		ID:              "job1",
		SourceURL:       "https://www.youtube.com/watch?v=abc123",
		PreferredHeight: 720,
		RequestedBy:     "u1",
		State:           domain.JobFailed,
		ErrorMessage:    "HTTP Error 429: Too Many Requests",
	}
}

func TestRetryQueuesTheSameUrlAgain(t *testing.T) {
	// The reason this exists: the usual cause of a failure here is temporary —
	// a rate limit, a block that has lifted, a network that dropped — and
	// before this the only way to try again was to go and find the video.
	store := &recordingStore{job: failedJob()}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	job, err := i.RetryJob(context.Background(), "job1", "u2")
	if err != nil {
		t.Fatalf("RetryJob: %v", err)
	}

	if len(store.enqueued) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(store.enqueued))
	}
	queued := store.enqueued[0]
	if queued.SourceURL != failedJob().SourceURL {
		t.Errorf("queued %q, want the failed job's URL", queued.SourceURL)
	}
	// Carried over, because the retry is of the same request: asking again at a
	// different quality would be a different job wearing this one's name.
	if queued.PreferredHeight != 720 {
		t.Errorf("queued height %d, want the original 720", queued.PreferredHeight)
	}
	if queued.ID == "job1" {
		t.Error("the retry reused the old job's id; the record of what failed is the point of keeping it")
	}
	if job.ID != queued.ID {
		t.Errorf("returned job %q is not the one queued %q", job.ID, queued.ID)
	}
}

func TestRetryClearsTheFailureItActedOn(t *testing.T) {
	// Otherwise the page shows the failure and its retry side by side, and the
	// old one goes on asking to be dealt with after it has been.
	store := &recordingStore{job: failedJob()}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	if _, err := i.RetryJob(context.Background(), "job1", "u1"); err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	if len(store.dismissed) != 1 || store.dismissed[0] != "job1" {
		t.Fatalf("dismissed %v, want [job1]", store.dismissed)
	}
}

func TestRetrySucceedsEvenIfTheOldRowWillNotHide(t *testing.T) {
	// The work asked for is the retry. A row that stays visible is untidy; an
	// error here would mean the download was never queued, which is worse.
	store := &recordingStore{job: failedJob(), dismissEr: domain.ErrNotFound}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	if _, err := i.RetryJob(context.Background(), "job1", "u1"); err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	if len(store.enqueued) != 1 {
		t.Fatal("the retry was not queued")
	}
}

func TestRetryRefusesAJobThatHasNotFinished(t *testing.T) {
	// Retrying something already running would put two transfers of the same
	// video on the wire, which is the shape of request volume that had this
	// address blocked once already.
	running := failedJob()
	running.State = domain.JobRunning
	store := &recordingStore{job: running}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	if _, err := i.RetryJob(context.Background(), "job1", "u1"); err == nil {
		t.Fatal("a running job was retried")
	}
	if len(store.enqueued) != 0 {
		t.Fatal("a second transfer was queued for a running job")
	}
}

func TestRetryKeepsTheOriginalRequesterWhenNobodyIsNamed(t *testing.T) {
	store := &recordingStore{job: failedJob()}
	i := New(&captionDownloader{}, nil, store, &fakeLibrary{}, 1080, nil)

	if _, err := i.RetryJob(context.Background(), "job1", ""); err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	if store.enqueued[0].RequestedBy != "u1" {
		t.Errorf("requested_by = %q, want the original u1", store.enqueued[0].RequestedBy)
	}
}

func TestDismissNeedsAJobID(t *testing.T) {
	i := New(&captionDownloader{}, nil, &recordingStore{}, &fakeLibrary{}, 1080, nil)
	if err := i.DismissJob(context.Background(), ""); err == nil {
		t.Fatal("an empty id was accepted")
	}
}

// scanRecorder stands in for the history table.
type scanRecorder struct {
	saved  []domain.ScanResult
	retain time.Duration
	list   []domain.ScanResult
	total  int32
}

func (s *scanRecorder) RecordScan(_ context.Context, r domain.ScanResult, retain time.Duration) error {
	s.saved = append(s.saved, r)
	s.retain = retain
	return nil
}

func (s *scanRecorder) ListScans(context.Context, int32, int32) ([]domain.ScanResult, int32, error) {
	return s.list, s.total, nil
}

func (s *scanRecorder) ClearScans(context.Context) error { return nil }

func TestListScansIsEmptyRatherThanBrokenWithoutAStore(t *testing.T) {
	// The Activity page asks for these unconditionally; a deployment without
	// the table should show no history, not an error where the history goes.
	i := New(&captionDownloader{}, nil, &recordingStore{}, &fakeLibrary{}, 1080, nil)

	scans, total, err := i.ListScans(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("ListScans: %v", err)
	}
	if len(scans) != 0 || total != 0 {
		t.Fatalf("got %d scans (total %d), want none", len(scans), total)
	}
}

func TestListScansReadsTheStore(t *testing.T) {
	recorder := &scanRecorder{
		list:  []domain.ScanResult{{SourcesScanned: 63, VideosAdded: 4}},
		total: 240,
	}
	i := New(&captionDownloader{}, nil, &recordingStore{}, &fakeLibrary{}, 1080, nil)
	i.SetScanStore(recorder)

	scans, total, err := i.ListScans(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("ListScans: %v", err)
	}
	if len(scans) != 1 || scans[0].SourcesScanned != 63 {
		t.Fatalf("got %+v", scans)
	}
	// The total is what tells the page whether to offer more.
	if total != 240 {
		t.Fatalf("total = %d, want 240", total)
	}
}
