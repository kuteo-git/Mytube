package usecase

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

func TestVideoIDFromWatchURL(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=abc123":          "abc123",
		"https://www.youtube.com/watch?v=abc123&list=PL9": "abc123",
		"https://youtu.be/abc123":                         "abc123",
		"  https://www.youtube.com/watch?v=abc123  ":      "abc123",
		"https://www.youtube.com/@channel/videos":         "",
		"": "",
	}
	for raw, want := range cases {
		if got := videoIDFromURL(raw); got != want {
			t.Errorf("videoIDFromURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// captionDownloader records how many caption fetches were started, and can be
// held mid-fetch so a second request arrives while the first is still running.
type captionDownloader struct {
	fakeDownloader
	mu      sync.Mutex
	started int
	release chan struct{}
	entered chan struct{}
}

func (c *captionDownloader) FetchSubtitles(context.Context, string, string, int32) []domain.SubtitleTrack {
	c.mu.Lock()
	c.started++
	c.mu.Unlock()
	if c.entered != nil {
		c.entered <- struct{}{}
	}
	if c.release != nil {
		<-c.release
	}
	return nil
}

func (c *captionDownloader) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.started
}

func newSubtitleIngest(d domain.Downloader) *Ingest {
	return New(d, nil, &fakeStore{}, &fakeLibrary{}, 1080, nil)
}

func TestPressingPlayStartsTheCaptionFetch(t *testing.T) {
	d := &captionDownloader{entered: make(chan struct{}, 1), release: make(chan struct{})}
	i := newSubtitleIngest(d)

	if _, err := i.Submit(context.Background(), "https://www.youtube.com/watch?v=abc123", "u1", 1080); err != nil {
		t.Fatal(err)
	}

	select {
	case <-d.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("captions were never fetched")
	}
	close(d.release)
}

func TestASecondPressDoesNotStartASecondFetch(t *testing.T) {
	// Two people opening the same video, or one person pressing play twice,
	// must not become two sets of requests upstream — this address has been
	// blocked by YouTube once already for asking too often.
	d := &captionDownloader{entered: make(chan struct{}, 1), release: make(chan struct{})}
	i := newSubtitleIngest(d)
	url := "https://www.youtube.com/watch?v=abc123"

	if _, err := i.Submit(context.Background(), url, "u1", 1080); err != nil {
		t.Fatal(err)
	}
	<-d.entered // the first fetch is now in flight and held there

	if _, err := i.Submit(context.Background(), url, "u2", 1080); err != nil {
		t.Fatal(err)
	}

	close(d.release)
	time.Sleep(50 * time.Millisecond)

	if got := d.count(); got != 1 {
		t.Fatalf("caption fetches = %d, want 1", got)
	}
}

func TestAUrlWithNoRecoverableIDIsLeftToTheWorker(t *testing.T) {
	d := &captionDownloader{}
	i := newSubtitleIngest(d)

	if _, err := i.Submit(context.Background(), "https://example.com/something", "u1", 1080); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)

	if got := d.count(); got != 0 {
		t.Fatalf("caption fetches = %d, want 0 — nothing here names a video", got)
	}
}
