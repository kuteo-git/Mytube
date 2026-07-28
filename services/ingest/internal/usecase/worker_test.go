package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// fakeDownloader records the order in which the worker asks for things.
type fakeDownloader struct {
	calls []string
}

func (f *fakeDownloader) Search(context.Context, string, int32) ([]domain.ExternalVideo, error) {
	return nil, nil
}

func (f *fakeDownloader) Preview(context.Context, string) (domain.ExternalVideo, error) {
	f.calls = append(f.calls, "preview")
	return domain.ExternalVideo{ID: "vid1", Title: "Test"}, nil
}

func (f *fakeDownloader) ListPlaylist(context.Context, string, int32, int32) (string, []domain.ExternalVideo, error) {
	return "", nil, nil
}

func (f *fakeDownloader) ResolveStream(context.Context, string) (domain.StreamLocation, error) {
	return domain.StreamLocation{}, nil
}

func (f *fakeDownloader) FetchSubtitles(context.Context, string, string, int32) []domain.SubtitleTrack {
	f.calls = append(f.calls, "subtitles")
	return []domain.SubtitleTrack{{Language: "en", Label: "English", Path: "vid1/1080p.en.vtt"}}
}

func (f *fakeDownloader) Download(context.Context, string, string, int32, func(domain.Progress)) (domain.DownloadResult, error) {
	f.calls = append(f.calls, "download")
	return domain.DownloadResult{MediaPath: "vid1/1080p.mp4", SizeBytes: 1234}, nil
}

func (f *fakeDownloader) ChannelInfo(context.Context, string) (domain.ChannelMetadata, error) {
	return domain.ChannelMetadata{}, nil
}

func (f *fakeDownloader) FetchChannelArtwork(context.Context, domain.ChannelMetadata) (string, string) {
	return "", ""
}

// fakeLibrary records every media-state write so the test can assert that
// subtitles were published before the media file existed.
type fakeLibrary struct {
	states    []string
	subtitles [][]domain.SubtitleTrack
}

func (f *fakeLibrary) FindBySourceURL(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeLibrary) UpsertChannel(context.Context, domain.ExternalVideo) error { return nil }
func (f *fakeLibrary) UpsertVideo(context.Context, domain.ExternalVideo, string) error {
	return nil
}
func (f *fakeLibrary) SourceURLFor(context.Context, string) (string, error) { return "", nil }
func (f *fakeLibrary) UpsertChannelArtwork(context.Context, domain.ChannelMetadata, string, string) error {
	return nil
}
func (f *fakeLibrary) ListSubscribedChannels(context.Context) ([]domain.SubscribedChannel, error) {
	return nil, nil
}
func (f *fakeLibrary) SetMediaState(_ context.Context, _, state, _ string, _ int64, subs []domain.SubtitleTrack) error {
	f.states = append(f.states, state)
	f.subtitles = append(f.subtitles, subs)
	return nil
}

// fakeStore is a no-op job store; the worker's queue behaviour is not under test.
type fakeStore struct{}

func (fakeStore) Enqueue(_ context.Context, j domain.Job) (domain.Job, error) { return j, nil }
func (fakeStore) Get(context.Context, string) (domain.Job, error)             { return domain.Job{}, nil }
func (fakeStore) List(context.Context, bool, int32) ([]domain.Job, error)     { return nil, nil }
func (fakeStore) Cancel(context.Context, string) error                       { return nil }
func (fakeStore) Claim(context.Context, time.Duration) (domain.Job, error) {
	return domain.Job{}, domain.ErrNotFound
}
func (fakeStore) Heartbeat(context.Context, string, time.Duration, domain.Progress) error {
	return nil
}
func (fakeStore) MarkResolved(context.Context, string, string, string) error { return nil }
func (fakeStore) Finish(context.Context, string, domain.JobState, string) error {
	return nil
}
func (fakeStore) ReleaseExpired(context.Context) (int, error) { return 0, nil }

func TestProcessPublishesSubtitlesBeforeMedia(t *testing.T) {
	downloader := &fakeDownloader{}
	library := &fakeLibrary{}
	ingest := New(downloader, nil, fakeStore{}, library, 1080, nil)
	worker := NewWorker(ingest, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := worker.process(context.Background(), domain.Job{
		ID:              "job1",
		SourceURL:       "https://example.test/watch?v=vid1",
		PreferredHeight: 1080,
	})
	if err != nil {
		t.Fatalf("process: %v", err)
	}

	// Captions must be requested before the transfer, not after it.
	want := []string{"preview", "subtitles", "download"}
	if len(downloader.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", downloader.calls, want)
	}
	for i := range want {
		if downloader.calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", downloader.calls, want)
		}
	}

	// The first media-state write must carry the subtitles while the video is
	// still downloading; that is what makes captions available during upstream
	// playback.
	if len(library.states) != 2 {
		t.Fatalf("media state writes = %v, want 2", library.states)
	}
	if library.states[0] != "DOWNLOADING" {
		t.Errorf("first write state = %q, want DOWNLOADING", library.states[0])
	}
	if len(library.subtitles[0]) != 1 {
		t.Errorf("first write carried %d subtitles, want 1", len(library.subtitles[0]))
	}
	if library.states[1] != "READY" {
		t.Errorf("second write state = %q, want READY", library.states[1])
	}
}
