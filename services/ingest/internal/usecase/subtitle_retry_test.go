package usecase

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// refusingDownloader answers the way yt-dlp does when upstream turns the
// caption endpoint away: no tracks, no error, and `refused` to say which of the
// two identical-looking outcomes this was.
type captionRefuser struct {
	domain.Downloader
	mu       sync.Mutex
	calls    int
	refuse   bool
	returned []domain.SubtitleTrack
}

func (d *captionRefuser) FetchSubtitles(context.Context, string, string, int32) ([]domain.SubtitleTrack, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	return d.returned, d.refuse
}

// retryStore records what the usecase asks it to remember.
type retryStore struct {
	fakeStore
	mu        sync.Mutex
	recorded  []domain.SubtitleRetry
	cleared   []string
	clearedID []string
	due       *domain.SubtitleRetry
	askedWith []time.Duration
}

func (s *retryStore) RecordSubtitleRefusal(_ context.Context, r domain.SubtitleRetry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorded = append(s.recorded, r)
	return nil
}

func (s *retryStore) ClearSubtitleRetry(_ context.Context, url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleared = append(s.cleared, url)
	return nil
}

func (s *retryStore) ClearSubtitleRetryForVideo(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearedID = append(s.clearedID, id)
	return nil
}

func (s *retryStore) DueSubtitleRetry(_ context.Context, backoff []time.Duration) (domain.SubtitleRetry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.askedWith = backoff
	if s.due == nil {
		return domain.SubtitleRetry{}, false, nil
	}
	return *s.due, true, nil
}

func retryIngest(d domain.Downloader, s *retryStore) *Ingest {
	return New(d, nil, s, &fakeLibrary{}, 1080, nil)
}

const captionURL = "https://www.youtube.com/watch?v=abc123"

/*
The reported fault, twice: a video played with no captions, and nothing ever
asked again. The caption endpoint refuses this address in waves — measured with
plain yt-dlp outside this app, the same video and the same flags succeeding and
failing minutes apart — and every attempt used to happen while somebody was
watching, four of them over about ninety seconds.
*/
func TestARefusalIsRememberedRatherThanForgotten(t *testing.T) {
	store := &retryStore{}
	i := retryIngest(&captionRefuser{refuse: true}, store)

	i.fetchAndPublishSubtitles(context.Background(), captionURL, "abc123", 720)

	if len(store.recorded) != 1 {
		t.Fatalf("recorded %d refusals, want 1", len(store.recorded))
	}
	got := store.recorded[0]
	if got.SourceURL != captionURL || got.VideoID != "abc123" {
		t.Errorf("recorded %+v, want the video that was refused", got)
	}
	// The rendition travels with it: the caption filenames are derived from the
	// media target, so a retry at a different height writes different names.
	if got.Height != 720 {
		t.Errorf("recorded height %d, want 720", got.Height)
	}
	if len(store.cleared) != 0 {
		t.Errorf("cleared %v on a refusal, want nothing cleared", store.cleared)
	}
}

// A video that simply has no captions is finished. Recording it would have the
// sweep spend four more extracts on an answer that will not change — the
// counted kind of request, against the address CLAUDE.md §8 risk 6 is about.
func TestNoCaptionsIsNotARefusal(t *testing.T) {
	store := &retryStore{}
	i := retryIngest(&captionRefuser{refuse: false}, store)

	i.fetchAndPublishSubtitles(context.Background(), captionURL, "abc123", 1080)

	if len(store.recorded) != 0 {
		t.Errorf("recorded %+v, want nothing recorded", store.recorded)
	}
	if len(store.cleared) != 1 {
		t.Errorf("cleared %v, want the row dropped", store.cleared)
	}
}

func TestCaptionsLandingForgetTheRetry(t *testing.T) {
	store := &retryStore{}
	d := &captionRefuser{returned: []domain.SubtitleTrack{{Language: "en", Path: "abc123/1080p.mp4.en.vtt"}}}
	i := retryIngest(d, store)

	i.fetchAndPublishSubtitles(context.Background(), captionURL, "abc123", 1080)

	if len(store.cleared) != 1 || store.cleared[0] != captionURL {
		t.Errorf("cleared %v, want %q", store.cleared, captionURL)
	}
}

func TestTheSweepAsksAgainForOneVideo(t *testing.T) {
	store := &retryStore{due: &domain.SubtitleRetry{SourceURL: captionURL, VideoID: "abc123", Height: 720, Attempts: 2}}
	d := &captionRefuser{returned: []domain.SubtitleTrack{{Language: "en"}}}
	i := retryIngest(d, store)

	backoff := []time.Duration{time.Minute, 5 * time.Minute}
	i.RetrySubtitlesOnce(context.Background(), backoff)

	d.mu.Lock()
	calls := d.calls
	d.mu.Unlock()
	if calls != 1 {
		t.Fatalf("fetched %d times, want 1", calls)
	}
	if len(store.askedWith) != len(backoff) {
		t.Errorf("asked with %v, want the worker's backoff", store.askedWith)
	}
}

// Nothing due must cost nothing. The sweep runs once a minute for the life of
// the process, and a fetch on an empty queue is a full extract for no reason.
func TestTheCaptionSweepDoesNothingWhenNothingIsDue(t *testing.T) {
	store := &retryStore{}
	d := &captionRefuser{}
	i := retryIngest(d, store)

	i.RetrySubtitlesOnce(context.Background(), []time.Duration{time.Minute})

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.calls != 0 {
		t.Errorf("fetched %d times with nothing due, want 0", d.calls)
	}
}

// Leaving the watch page means nobody is waiting for this video, and the route
// that says so already exists for the download. The caption queue was ignoring
// it: a video opened by mistake and left after three seconds went on being
// asked about every few hours for ever, against an address YouTube was already
// refusing. Nothing is lost — pressing play starts the fetch again.
func TestLeavingTheVideoStopsAskingForItsCaptions(t *testing.T) {
	store := &retryStore{}
	i := retryIngest(&captionRefuser{}, store)

	if _, err := i.CancelVideoDownload(context.Background(), "abc123"); err != nil {
		t.Fatal(err)
	}

	if len(store.clearedID) != 1 || store.clearedID[0] != "abc123" {
		t.Errorf("cleared %v, want the video that was left", store.clearedID)
	}
}
