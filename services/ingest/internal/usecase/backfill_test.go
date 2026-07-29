package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"

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
	return New(previews, nil, nil, library, 1080, nil), library
}

func TestBackfillWritesTheYouTubeCategoryAsTheTopic(t *testing.T) {
	previews := &previewDownloader{byURL: map[string]domain.ExternalVideo{
		"https://www.youtube.com/watch?v=abc": {ID: "abc", Title: "A", Category: "Music"},
		"https://www.youtube.com/watch?v=def": {ID: "def", Title: "B", Category: "Gaming"},
	}}
	ingest, library := newBackfillIngest(t, []domain.VideoRef{
		{VideoID: "abc"}, {VideoID: "def"},
	}, previews)

	result, err := ingest.BackfillTopics(context.Background(), 0)
	if err != nil {
		t.Fatalf("BackfillTopics: %v", err)
	}
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

	if _, err := ingest.BackfillTopics(context.Background(), 0); err != nil {
		t.Fatalf("BackfillTopics: %v", err)
	}
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

	if _, err := ingest.BackfillTopics(context.Background(), 0); err != nil {
		t.Fatalf("BackfillTopics: %v", err)
	}
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

	result, err := ingest.BackfillTopics(context.Background(), 0)
	if err != nil {
		t.Fatalf("BackfillTopics: %v", err)
	}
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

	result, err := ingest.BackfillTopics(context.Background(), 0)
	if err != nil {
		t.Fatalf("a dead video must not fail the pass: %v", err)
	}
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

	result, err := ingest.BackfillTopics(context.Background(), 0)
	if err != nil {
		t.Fatalf("BackfillTopics: %v", err)
	}
	if result.Examined != 0 || result.Updated != 0 {
		t.Fatalf("expected an empty result, got %+v", result)
	}
	if len(previews.requested) != 0 {
		t.Fatalf("fetched %v with nothing to do", previews.requested)
	}
}
