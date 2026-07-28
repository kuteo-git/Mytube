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

// categoryDownloader lets a test control what ListPlaylist and Preview return,
// and counts Preview calls so a test can assert the no-cost-for-known-videos
// guarantee: an already-known video must never trigger a full metadata fetch.
type categoryDownloader struct {
	listed       []domain.ExternalVideo
	previewByURL map[string]domain.ExternalVideo
	previewErr   error
	previewCalls []string
}

func (d *categoryDownloader) Search(context.Context, string, int32) ([]domain.ExternalVideo, error) {
	return nil, nil
}
func (d *categoryDownloader) Preview(_ context.Context, url string) (domain.ExternalVideo, error) {
	d.previewCalls = append(d.previewCalls, url)
	if d.previewErr != nil {
		return domain.ExternalVideo{}, d.previewErr
	}
	return d.previewByURL[url], nil
}
func (d *categoryDownloader) ListPlaylist(context.Context, string, int32, int32) (string, []domain.ExternalVideo, error) {
	return "", d.listed, nil
}
func (d *categoryDownloader) ResolveStream(context.Context, string) (domain.StreamLocation, error) {
	return domain.StreamLocation{}, nil
}
func (d *categoryDownloader) FetchSubtitles(context.Context, string, string, int32) []domain.SubtitleTrack {
	return nil
}
func (d *categoryDownloader) Download(context.Context, string, string, int32, func(domain.Progress)) (domain.DownloadResult, error) {
	return domain.DownloadResult{}, nil
}
func (d *categoryDownloader) ChannelInfo(context.Context, string) (domain.ChannelMetadata, error) {
	return domain.ChannelMetadata{}, nil
}
func (d *categoryDownloader) FetchChannelArtwork(context.Context, domain.ChannelMetadata) (string, string) {
	return "", ""
}

type categoryLibrary struct {
	known         map[string]bool
	upsertedTopic map[string][]string // sourceURL -> topics written
}

func (l *categoryLibrary) FindBySourceURL(_ context.Context, url string) (string, bool, error) {
	return "", l.known[url], nil
}
func (l *categoryLibrary) UpsertChannel(context.Context, domain.ExternalVideo) error { return nil }
func (l *categoryLibrary) UpsertVideo(_ context.Context, v domain.ExternalVideo, _ string) error {
	l.upsertedTopic[v.SourceURL] = v.Topics
	return nil
}
func (l *categoryLibrary) SetMediaState(context.Context, string, string, string, int64, []domain.SubtitleTrack) error {
	return nil
}
func (l *categoryLibrary) SourceURLFor(context.Context, string) (string, error) { return "", nil }
func (l *categoryLibrary) UpsertChannelArtwork(context.Context, domain.ChannelMetadata, string, string) error {
	return nil
}
func (l *categoryLibrary) ListSubscribedChannels(context.Context) ([]domain.SubscribedChannel, error) {
	return nil, nil
}

func newScanner(d domain.Downloader, l domain.Library) *Scanner {
	return NewScanner(stubTopics{}, d, l, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Hour)
}

func TestNewVideoIsFiledUnderItsYouTubeCategory(t *testing.T) {
	url := "https://youtube.test/watch?v=new1"
	downloader := &categoryDownloader{
		listed:       []domain.ExternalVideo{{ID: "new1", SourceURL: url}},
		previewByURL: map[string]domain.ExternalVideo{url: {Category: "Science & Technology"}},
	}
	library := &categoryLibrary{known: map[string]bool{}, upsertedTopic: map[string][]string{}}
	scanner := newScanner(downloader, library)

	_, added, err := scanner.scanSource(context.Background(), "Tech", "https://youtube.test/@a/videos", 10)
	if err != nil {
		t.Fatalf("scanSource: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if got := library.upsertedTopic[url]; len(got) != 1 || got[0] != "Science & Technology" {
		t.Fatalf("topics = %v, want [Science & Technology]", got)
	}
	if len(downloader.previewCalls) != 1 {
		t.Fatalf("Preview called %d times, want 1", len(downloader.previewCalls))
	}
}

// The cost guarantee this whole design depends on: re-scanning a channel must
// not re-fetch full metadata for videos the library has already seen.
func TestAlreadyKnownVideoIsNeverRePreviewed(t *testing.T) {
	url := "https://youtube.test/watch?v=old1"
	downloader := &categoryDownloader{
		listed: []domain.ExternalVideo{{ID: "old1", SourceURL: url}},
	}
	library := &categoryLibrary{known: map[string]bool{url: true}, upsertedTopic: map[string][]string{}}
	scanner := newScanner(downloader, library)

	_, added, err := scanner.scanSource(context.Background(), "Tech", "https://youtube.test/@a/videos", 10)
	if err != nil {
		t.Fatalf("scanSource: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if len(downloader.previewCalls) != 0 {
		t.Fatalf("Preview called %d times for an already-known video, want 0", len(downloader.previewCalls))
	}
}

func TestCategoryFetchFailureFallsBackToTheCuratedTopicName(t *testing.T) {
	url := "https://youtube.test/watch?v=new2"
	downloader := &categoryDownloader{
		listed:     []domain.ExternalVideo{{ID: "new2", SourceURL: url}},
		previewErr: errors.New("network blip"),
	}
	library := &categoryLibrary{known: map[string]bool{}, upsertedTopic: map[string][]string{}}
	scanner := newScanner(downloader, library)

	_, added, err := scanner.scanSource(context.Background(), "Tech", "https://youtube.test/@a/videos", 10)
	if err != nil {
		t.Fatalf("scanSource: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if got := library.upsertedTopic[url]; len(got) != 1 || got[0] != "Tech" {
		t.Fatalf("topics = %v, want [Tech] (fallback)", got)
	}
}

func TestCategoryFetchFailureLeavesASubscriptionVideoTopicless(t *testing.T) {
	url := "https://youtube.test/watch?v=new3"
	downloader := &categoryDownloader{
		listed:     []domain.ExternalVideo{{ID: "new3", SourceURL: url}},
		previewErr: errors.New("network blip"),
	}
	library := &categoryLibrary{known: map[string]bool{}, upsertedTopic: map[string][]string{}}
	scanner := newScanner(downloader, library)

	// Empty topicName marks a subscription source — no curated name to fall
	// back to.
	_, added, err := scanner.scanSource(context.Background(), "", "https://youtube.test/@sub/videos", 10)
	if err != nil {
		t.Fatalf("scanSource: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if got := library.upsertedTopic[url]; len(got) != 0 {
		t.Fatalf("topics = %v, want none", got)
	}
}
