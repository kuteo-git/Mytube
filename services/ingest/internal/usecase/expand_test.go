package usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

type recordingLibrary struct {
	added  []string
	known  map[string]bool
	topics map[string][]string // videoID -> topics written
	// channels records the whole video as written, so a test can assert which
	// channel it was attributed to.
	channels map[string]domain.ExternalVideo
	// missingTopics is what ListVideosNeedingBackfill hands back, so a backfill
	// test can control exactly which videos a pass sees.
	missingTopics []domain.VideoRef
}

func (r *recordingLibrary) FindBySourceURL(_ context.Context, url string) (string, bool, error) {
	return "", r.known[url], nil
}
func (r *recordingLibrary) UpsertChannel(context.Context, domain.ExternalVideo) error { return nil }
func (r *recordingLibrary) UpsertVideo(_ context.Context, v domain.ExternalVideo, _ string) error {
	r.added = append(r.added, v.ID)
	if r.topics != nil {
		r.topics[v.ID] = v.Topics
	}
	if r.channels != nil {
		r.channels[v.ID] = v
	}
	return nil
}
func (r *recordingLibrary) SetMediaState(context.Context, string, string, string, int64, []domain.SubtitleTrack) error {
	return nil
}
func (r *recordingLibrary) SourceURLFor(context.Context, string) (string, error) { return "", nil }
func (r *recordingLibrary) UpsertChannelArtwork(context.Context, domain.ChannelMetadata, string, string) error {
	return nil
}
func (r *recordingLibrary) ListSubscribedChannels(context.Context) ([]domain.SubscribedChannel, error) {
	return nil, nil
}
func (r *recordingLibrary) ListVideosNeedingBackfill(context.Context, int32) ([]domain.VideoRef, error) {
	return r.missingTopics, nil
}

type stubCursors struct{ offsets map[string]int32 }

func (s *stubCursors) NextOffset(_ context.Context, url string) (int32, error) {
	return s.offsets[url], nil
}
func (s *stubCursors) AdvanceOffset(_ context.Context, url string, by int32) error {
	s.offsets[url] += by
	return nil
}

type stubTopics struct{}

func (stubTopics) Load(context.Context) (domain.TopicConfig, error) {
	return domain.TopicConfig{
		Topics: []domain.Topic{{
			Name:    "Tech",
			Sources: []string{"https://youtube.test/@a/videos"},
		}},
		PerSourceLimit: 2,
	}, nil
}

type deepenDownloader struct {
	offsetsAsked []int32
	previewByURL map[string]domain.ExternalVideo
	previewCalls []string
}

// Search returns nothing: this fake exists to isolate the deepen path, and a
// non-empty Search result would make Expand fall through to the search layer
// whenever deepen's yield is below expandTarget, contaminating what these
// tests are actually asserting about deepen and its cursor.
func (d *deepenDownloader) Search(context.Context, string, int32) ([]domain.ExternalVideo, error) {
	return nil, nil
}
func (d *deepenDownloader) Preview(_ context.Context, url string) (domain.ExternalVideo, error) {
	d.previewCalls = append(d.previewCalls, url)
	return d.previewByURL[url], nil
}
func (d *deepenDownloader) ListPlaylist(_ context.Context, _ string, offset, _ int32) (string, []domain.ExternalVideo, error) {
	d.offsetsAsked = append(d.offsetsAsked, offset)
	return "A", []domain.ExternalVideo{
		{ID: "deep1", SourceURL: "https://youtube.test/watch?v=deep1"},
		{ID: "deep2", SourceURL: "https://youtube.test/watch?v=deep2"},
	}, nil
}
func (d *deepenDownloader) ResolveStream(context.Context, string) (domain.StreamLocation, error) {
	return domain.StreamLocation{}, nil
}
func (d *deepenDownloader) FetchSubtitles(context.Context, string, string, int32) []domain.SubtitleTrack {
	return nil
}
func (d *deepenDownloader) Download(context.Context, string, string, int32, func(domain.Progress)) (domain.DownloadResult, error) {
	return domain.DownloadResult{}, nil
}
func (d *deepenDownloader) ChannelInfo(context.Context, string) (domain.ChannelMetadata, error) {
	return domain.ChannelMetadata{}, nil
}
func (d *deepenDownloader) FetchChannelArtwork(context.Context, domain.ChannelMetadata) (string, string) {
	return "", ""
}

func (d *deepenDownloader) SaveThumbnail(ctx context.Context, url, videoID string) string { return "" }

type failingRelated struct{}

func (failingRelated) Related(context.Context, string) ([]domain.ExternalVideo, error) {
	return nil, errors.New("innertube is down")
}

func newExpander(d domain.Downloader, related domain.RelatedSource, lib domain.Library, cursors domain.CursorStore) *Expander {
	return NewExpander(d, related, lib, stubTopics{}, cursors,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// Deepening comes first because it draws only on sources the user curated.
func TestExpandDeepensCuratedSourcesBeforeAnythingElse(t *testing.T) {
	downloader := &deepenDownloader{}
	library := &recordingLibrary{known: map[string]bool{}}
	cursors := &stubCursors{offsets: map[string]int32{"https://youtube.test/@a/videos": 40}}

	expander := newExpander(downloader, failingRelated{}, library, cursors)

	added, err := expander.Expand(context.Background(), "Tech", nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	if len(downloader.offsetsAsked) != 1 || downloader.offsetsAsked[0] != 40 {
		t.Fatalf("offsets asked = %v, want [40] — the cursor must advance past what was already scanned", downloader.offsetsAsked)
	}
	if cursors.offsets["https://youtube.test/@a/videos"] != 42 {
		t.Errorf("cursor = %d, want 42", cursors.offsets["https://youtube.test/@a/videos"])
	}
}

// The whole point of layering: InnerTube can vanish without taking the feed
// with it.
func TestExpandSurvivesRelatedSourceFailure(t *testing.T) {
	library := &recordingLibrary{known: map[string]bool{}}
	expander := newExpander(&deepenDownloader{}, failingRelated{}, library,
		&stubCursors{offsets: map[string]int32{}})

	if _, err := expander.Expand(context.Background(), "Tech", []string{"seed1"}); err != nil {
		t.Fatalf("a failing related source must not fail the expansion: %v", err)
	}
}

// Videos already in the library must not be written again.
func TestExpandSkipsVideosAlreadyPresent(t *testing.T) {
	library := &recordingLibrary{known: map[string]bool{
		"https://youtube.test/watch?v=deep1": true,
	}}
	expander := newExpander(&deepenDownloader{}, failingRelated{}, library,
		&stubCursors{offsets: map[string]int32{}})

	added, err := expander.Expand(context.Background(), "Tech", nil)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1", added)
	}
	if len(library.added) != 1 || library.added[0] != "deep2" {
		t.Fatalf("added %v, want [deep2]", library.added)
	}
}

// Expansion must stay cheap: it walks flat listings only and never pays for a
// full per-video metadata fetch. The category that fills in a video's topic
// arrives later, free, from the Preview that EnsureVideo or the download worker
// already performs — see CLAUDE.md §7.
func TestExpandNeverFetchesFullMetadata(t *testing.T) {
	downloader := &deepenDownloader{}
	library := &recordingLibrary{known: map[string]bool{}, topics: map[string][]string{}}
	expander := newExpander(downloader, failingRelated{}, library, &stubCursors{offsets: map[string]int32{}})

	if _, err := expander.Expand(context.Background(), "Tech", nil); err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(downloader.previewCalls) != 0 {
		t.Fatalf("Preview called %d times during expansion, want 0: %v",
			len(downloader.previewCalls), downloader.previewCalls)
	}
}

// deepen files videos under the curated source's topic; that is the one place
// a topic is known without asking YouTube anything.
func TestExpandFilesDeepenedVideosUnderTheCuratedTopic(t *testing.T) {
	library := &recordingLibrary{known: map[string]bool{}, topics: map[string][]string{}}
	expander := newExpander(&deepenDownloader{}, failingRelated{}, library,
		&stubCursors{offsets: map[string]int32{}})

	if _, err := expander.Expand(context.Background(), "Tech", nil); err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if got := library.topics["deep1"]; len(got) != 1 || got[0] != "Tech" {
		t.Fatalf("topics = %v, want [Tech]", got)
	}
}
