package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// categoryDownloader returns a video carrying YouTube's own category, as a real
// full-metadata fetch does. Flat listings never carry one, which is why the
// topic can only be learned here.
type categoryDownloader struct{ deepenDownloader }

func (d *categoryDownloader) Preview(_ context.Context, url string) (domain.ExternalVideo, error) {
	d.previewCalls = append(d.previewCalls, url)
	return domain.ExternalVideo{
		ID:        "vid1",
		Title:     "Test",
		SourceURL: url,
		Category:  "Science & Technology",
	}, nil
}

// Opening a video from search is one of the two places that already pays for
// full metadata, so the category — and therefore the topic — is free here.
func TestEnsureVideoFilesUnderTheYouTubeCategory(t *testing.T) {
	library := &recordingLibrary{known: map[string]bool{}, topics: map[string][]string{}}
	ingest := New(&categoryDownloader{}, fakeStore{}, library, 1080)

	id, err := ingest.EnsureVideo(context.Background(), "https://youtube.test/watch?v=vid1")
	if err != nil {
		t.Fatalf("EnsureVideo: %v", err)
	}
	if id != "vid1" {
		t.Fatalf("id = %q, want vid1", id)
	}
	if got := library.topics["vid1"]; len(got) != 1 || got[0] != "Science & Technology" {
		t.Fatalf("topics = %v, want [Science & Technology]", got)
	}
}

// Downloading is the other place full metadata is already fetched.
func TestDownloadWorkerFilesUnderTheYouTubeCategory(t *testing.T) {
	library := &recordingLibrary{known: map[string]bool{}, topics: map[string][]string{}}
	ingest := New(&categoryDownloader{}, fakeStore{}, library, 1080)
	worker := NewWorker(ingest, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := worker.process(context.Background(), domain.Job{
		ID:              "job1",
		SourceURL:       "https://youtube.test/watch?v=vid1",
		PreferredHeight: 1080,
	})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if got := library.topics["vid1"]; len(got) != 1 || got[0] != "Science & Technology" {
		t.Fatalf("topics = %v, want [Science & Technology]", got)
	}
}

// A video with no category must keep whatever topic it already had rather than
// having it cleared — a scan may have already filed it under a curated topic.
func TestNoCategoryLeavesExistingTopicsAlone(t *testing.T) {
	v := domain.ExternalVideo{Topics: []string{"Tech"}}
	if got := categoryTopics(v); len(got) != 1 || got[0] != "Tech" {
		t.Fatalf("topics = %v, want [Tech]", got)
	}
}
