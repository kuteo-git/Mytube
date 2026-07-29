package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// previewDownloader answers Preview from a table and records what it was asked.
type previewDownloader struct {
	// Embedded only to satisfy the rest of the Downloader surface; every method
	// this test cares about is overridden below.
	deepenDownloader

	mu        sync.Mutex
	byURL     map[string]domain.ExternalVideo
	failFor   map[string]bool
	requested []string
}

func (p *previewDownloader) Preview(_ context.Context, url string) (domain.ExternalVideo, error) {
	p.mu.Lock()
	p.requested = append(p.requested, url)
	p.mu.Unlock()

	if p.failFor[url] {
		return domain.ExternalVideo{}, errors.New("video unavailable")
	}
	video, ok := p.byURL[url]
	if !ok {
		return domain.ExternalVideo{}, errors.New("not found")
	}
	return video, nil
}

// runBackfillToCompletion starts a pass and waits for it, because the pass is
// asynchronous by design: a full one runs for hours against YouTube's throttle,
// far longer than any HTTP request can be held open.
func runBackfillToCompletion(t *testing.T, ingest *Ingest, limit int32) BackfillResult {
	t.Helper()
	if _, err := ingest.BackfillTopics(context.Background(), limit); err != nil {
		t.Fatalf("BackfillTopics: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status := ingest.BackfillStatus()
		if !status.Running {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("backfill did not finish within the deadline")
	return BackfillResult{}
}

func newBackfillIngest(
	t *testing.T, refs []domain.VideoRef, previews *previewDownloader,
) (*Ingest, *recordingLibrary) {
	t.Helper()
	library := &recordingLibrary{
		known:         map[string]bool{},
		topics:        map[string][]string{},
		channels:      map[string]domain.ExternalVideo{},
		missingTopics: refs,
	}
	ingest := New(previews, nil, nil, library, 1080, nil)
	// The real pass waits four seconds between fetches to stay under YouTube's
	// bot detection. Tests have no such audience.
	ingest.backfillDelay = time.Microsecond
	return ingest, library
}

func TestBackfillWritesTheYouTubeCategoryAsTheTopic(t *testing.T) {
	previews := &previewDownloader{byURL: map[string]domain.ExternalVideo{
		"https://www.youtube.com/watch?v=abc": {ID: "abc", Title: "A", Category: "Music"},
		"https://www.youtube.com/watch?v=def": {ID: "def", Title: "B", Category: "Gaming"},
	}}
	ingest, library := newBackfillIngest(t, []domain.VideoRef{
		{VideoID: "abc"}, {VideoID: "def"},
	}, previews)

	result := runBackfillToCompletion(t, ingest, 0)
	if result.Updated != 2 || result.Failed != 0 {
		t.Fatalf("updated=%d failed=%d, want 2 and 0", result.Updated, result.Failed)
	}
	if got := library.topics["abc"]; len(got) != 1 || got[0] != "Music" {
		t.Fatalf("topics for abc = %v, want [Music]", got)
	}
	if got := library.topics["def"]; len(got) != 1 || got[0] != "Gaming" {
		t.Fatalf("topics for def = %v, want [Gaming]", got)
	}
}

func TestBackfillReconstructsTheWatchURLFromTheVideoID(t *testing.T) {
	// The catalogue projection carries no source URL, so the pass builds one.
	// Getting this wrong means every fetch 404s and the whole run reports
	// failures rather than saying it could not address the videos.
	previews := &previewDownloader{byURL: map[string]domain.ExternalVideo{
		"https://www.youtube.com/watch?v=xyz": {ID: "xyz", Category: "Music"},
	}}
	ingest, _ := newBackfillIngest(t, []domain.VideoRef{{VideoID: "xyz"}}, previews)

	runBackfillToCompletion(t, ingest, 0)
	if len(previews.requested) != 1 ||
		previews.requested[0] != "https://www.youtube.com/watch?v=xyz" {
		t.Fatalf("requested %v", previews.requested)
	}
}

func TestBackfillKeepsTheCatalogueIDEvenIfPreviewReportsAnother(t *testing.T) {
	// Preview resolves the URL itself and a redirect can hand back a different
	// id. Trusting it would insert a second row instead of giving the video
	// that needed a topic one.
	previews := &previewDownloader{byURL: map[string]domain.ExternalVideo{
		"https://www.youtube.com/watch?v=wanted": {ID: "somethingelse", Category: "Music"},
	}}
	ingest, library := newBackfillIngest(t, []domain.VideoRef{{VideoID: "wanted"}}, previews)

	runBackfillToCompletion(t, ingest, 0)
	if _, ok := library.topics["wanted"]; !ok {
		t.Fatalf("wrote under the wrong id: %v", library.topics)
	}
	if _, ok := library.topics["somethingelse"]; ok {
		t.Fatal("preview's id was trusted over the catalogue's")
	}
}

func TestBackfillSkipsVideosYouTubePublishesNoCategoryFor(t *testing.T) {
	// Writing an empty topic list would be indistinguishable from never having
	// tried, and the next pass would fetch it all over again.
	previews := &previewDownloader{byURL: map[string]domain.ExternalVideo{
		"https://www.youtube.com/watch?v=plain": {ID: "plain", Category: ""},
	}}
	ingest, library := newBackfillIngest(t, []domain.VideoRef{{VideoID: "plain"}}, previews)

	result := runBackfillToCompletion(t, ingest, 0)
	if result.Updated != 0 || result.Failed != 1 {
		t.Fatalf("updated=%d failed=%d, want 0 and 1", result.Updated, result.Failed)
	}
	if len(library.added) != 0 {
		t.Fatalf("wrote %v despite having no category", library.added)
	}
}

func TestBackfillCarriesOnPastAnUnavailableVideo(t *testing.T) {
	// Private and removed videos are normal in a library assembled from
	// listings. One of them must not end the pass.
	previews := &previewDownloader{
		byURL: map[string]domain.ExternalVideo{
			"https://www.youtube.com/watch?v=good": {ID: "good", Category: "Music"},
		},
		failFor: map[string]bool{"https://www.youtube.com/watch?v=gone": true},
	}
	ingest, library := newBackfillIngest(t, []domain.VideoRef{
		{VideoID: "gone"}, {VideoID: "good"},
	}, previews)

	result := runBackfillToCompletion(t, ingest, 0)
	if result.Examined != 2 || result.Updated != 1 || result.Failed != 1 {
		t.Fatalf("examined=%d updated=%d failed=%d, want 2, 1, 1",
			result.Examined, result.Updated, result.Failed)
	}
	if _, ok := library.topics["good"]; !ok {
		t.Fatal("the reachable video was not updated")
	}
}

func TestBackfillWithNothingToDoIsCheapAndSilent(t *testing.T) {
	previews := &previewDownloader{byURL: map[string]domain.ExternalVideo{}}
	ingest, _ := newBackfillIngest(t, nil, previews)

	result := runBackfillToCompletion(t, ingest, 0)
	if result.Examined != 0 || result.Updated != 0 {
		t.Fatalf("expected an empty result, got %+v", result)
	}
	if len(previews.requested) != 0 {
		t.Fatalf("fetched %v with nothing to do", previews.requested)
	}
}

func TestBackfillStopsWhenEverythingStartsFailing(t *testing.T) {
	// This is the regression for a real incident. An eight-worker pass fetched
	// full metadata for eight hundred videos and YouTube began answering every
	// request on this address with "Sign in to confirm you're not a bot" — a
	// block that also took out stream resolution, so nothing outside the already
	// downloaded files could be played. Pushing on through a block only extends
	// it, so a run of consecutive failures has to end the pass.
	failing := &previewDownloader{
		byURL:   map[string]domain.ExternalVideo{},
		failFor: map[string]bool{},
	}
	refs := make([]domain.VideoRef, 0, 200)
	for i := 0; i < 200; i++ {
		id := "v" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		refs = append(refs, domain.VideoRef{VideoID: id})
		failing.failFor["https://www.youtube.com/watch?v="+id] = true
	}
	ingest, _ := newBackfillIngest(t, refs, failing)

	result := runBackfillToCompletion(t, ingest, 0)

	if result.Failed > backfillFailureCutoff+backfillConcurrency {
		t.Fatalf("kept going through %d failures, cutoff is %d",
			result.Failed, backfillFailureCutoff)
	}
	if len(failing.requested) > backfillFailureCutoff+backfillConcurrency {
		t.Fatalf("made %d requests against a source refusing all of them",
			len(failing.requested))
	}
}

func TestBackfillSuccessResetsTheFailureRun(t *testing.T) {
	// Dead videos are scattered through any listing. Only an unbroken run means
	// the source has stopped answering; counting them cumulatively would abandon
	// a healthy pass partway.
	previews := &previewDownloader{
		byURL:   map[string]domain.ExternalVideo{},
		failFor: map[string]bool{},
	}
	refs := make([]domain.VideoRef, 0, 60)
	for i := 0; i < 60; i++ {
		id := "v" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		refs = append(refs, domain.VideoRef{VideoID: id})
		url := "https://www.youtube.com/watch?v=" + id
		// Every third video is dead — far more than reality, and still not a run.
		if i%3 == 0 {
			previews.failFor[url] = true
		} else {
			previews.byURL[url] = domain.ExternalVideo{ID: id, Category: "Music"}
		}
	}
	ingest, _ := newBackfillIngest(t, refs, previews)

	result := runBackfillToCompletion(t, ingest, 0)

	if result.Updated+result.Failed != int32(len(refs)) {
		t.Fatalf("processed %d of %d videos; scattered failures ended the pass early",
			result.Updated+result.Failed, len(refs))
	}
}
