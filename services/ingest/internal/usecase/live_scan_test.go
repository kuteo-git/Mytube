package usecase

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// streamsDownloader records which URL was listed and answers with whatever
// entries the test sets.
type streamsDownloader struct {
	deepenDownloader
	listedURLs []string
	entries    []domain.ExternalVideo
	err        error
}

func (d *streamsDownloader) ListPlaylist(_ context.Context, url string, _, _ int32) (string, []domain.ExternalVideo, error) {
	d.listedURLs = append(d.listedURLs, url)
	if d.err != nil {
		return "", nil, d.err
	}
	return "A", d.entries, nil
}

func (d *streamsDownloader) FetchComments(context.Context, string) ([]domain.YouTubeComment, error) {
	return nil, nil
}

func (d *streamsDownloader) FetchChannelFeed(context.Context, string) ([]domain.RSSEntry, error) {
	return nil, nil
}

func newLiveScanner(fetch domain.Downloader, lib domain.Library) *Scanner {
	// channels is nil deliberately. Passing a ChannelSource would let listSource
	// prefer the browse API, and browse carries no live status at all — the
	// pass must never go near it.
	return NewScanner(stubTopics{}, fetch, nil, lib, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)), time.Hour)
}

// The measurement the whole feature rests on, held as a test.
//
// The /videos tab reports live_status on nothing and lists no broadcast at all;
// the /streams tab reports it on every entry. Measured on ABC News, one request
// each: /videos → 0 of 40, /streams → 1 is_live and 39 was_live.
//
// So the URL matters, and it is the kind of thing that silently reverts: the
// scanner's own channelUploadsURL builds /videos, it is one character away, and
// a pass that asked the wrong tab would run, cost the requests, log cheerfully
// and find nothing.
func TestLiveScanAsksTheStreamsTab(t *testing.T) {
	fetch := &streamsDownloader{}
	lib := &recordingLibrary{
		known:      map[string]bool{},
		subscribed: []domain.SubscribedChannel{{ID: "UC1", Handle: "@abc", Name: "ABC"}},
	}

	if err := newLiveScanner(fetch, lib).ScanLive(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(fetch.listedURLs) != 1 {
		t.Fatalf("listed %d URLs, want 1: %v", len(fetch.listedURLs), fetch.listedURLs)
	}
	if got := fetch.listedURLs[0]; got != "https://www.youtube.com/@abc/streams" {
		t.Errorf("listed %q, want the channel's /streams tab", got)
	}
}

// A scheduled broadcast is recorded, and a finished or ordinary video is not.
//
// Recording is not listing. "is_upcoming" never reaches the Live chip — that
// query asks for is_live — because pressing it plays nothing, and an item that
// does nothing when pressed is the dead control §5 forbids, here wearing the
// one badge that promises something is happening.
//
// But it has to be *stored*, and not storing it was its own fault. A scheduled
// broadcast appears in Home like any other video, and with nothing recorded the
// stream route had no idea: measured on mYPF7KARk5Q, a subscribed channel's
// stream, yt-dlp answers "This live event will begin in a few moments" while
// the app offered HLS and a mux built from adaptive tracks that do not exist
// yet, and the player showed a generic failure.
func TestLiveScanRecordsWhatIsOnAirAndWhatIsComing(t *testing.T) {
	fetch := &streamsDownloader{entries: []domain.ExternalVideo{
		{ID: "upcoming", LiveStatus: "is_upcoming"},
		{ID: "onair", LiveStatus: "is_live"},
		{ID: "finished", LiveStatus: "was_live"},
		{ID: "ordinary"},
	}}
	lib := &recordingLibrary{
		known:      map[string]bool{},
		channels:   map[string]domain.ExternalVideo{},
		subscribed: []domain.SubscribedChannel{{ID: "UC1", Handle: "@abc", Name: "ABC"}},
	}

	if err := newLiveScanner(fetch, lib).ScanLive(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A finished broadcast and an ordinary video are neither: the first is
	// already an ordinary video with an ordinary recording, and the second was
	// never anything else.
	if len(lib.added) != 2 {
		t.Fatalf("wrote %v, want the live one and the scheduled one only", lib.added)
	}
	got := map[string]string{}
	for id, v := range lib.channels {
		got[id] = v.LiveStatus
	}
	if got["onair"] != "is_live" {
		t.Errorf("on air recorded as %q", got["onair"])
	}
	if got["upcoming"] != "is_upcoming" {
		t.Errorf("scheduled recorded as %q — the stream route reads this to say\n"+
			"\"not started yet\" instead of offering tiers that cannot work", got["upcoming"])
	}
}

// A broadcast is attributed to the channel that was asked about, not to
// whatever the listing carried.
//
// A flat listing of a channel tab names the owner once, on the playlist, never
// per entry — the same gap upsertFromFeed already fills. Without this the
// broadcast is written with an empty channel id and vanishes from every read
// that joins on channels, which is all of them.
func TestLiveScanAttributesToTheChannelAsked(t *testing.T) {
	fetch := &streamsDownloader{entries: []domain.ExternalVideo{
		{ID: "onair", LiveStatus: "is_live"},
	}}
	lib := &recordingLibrary{
		known:      map[string]bool{},
		channels:   map[string]domain.ExternalVideo{},
		subscribed: []domain.SubscribedChannel{{ID: "UC1", Handle: "@abc", Name: "ABC"}},
	}

	if err := newLiveScanner(fetch, lib).ScanLive(context.Background()); err != nil {
		t.Fatal(err)
	}

	written := lib.channels["onair"]
	if written.ChannelID != "UC1" {
		t.Errorf("channel = %q, want the subscribed channel's id", written.ChannelID)
	}
	if written.LiveStatus != "is_live" {
		t.Errorf("live status = %q, want it carried through to the catalog", written.LiveStatus)
	}
}

// A channel that has never broadcast has no /streams tab, and there are far
// more of those than of the other kind. One must not stop the pass.
func TestLiveScanSurvivesAChannelWithNoStreamsTab(t *testing.T) {
	fetch := &streamsDownloader{err: context.DeadlineExceeded}
	lib := &recordingLibrary{
		known: map[string]bool{},
		subscribed: []domain.SubscribedChannel{
			{ID: "UC1", Handle: "@a"}, {ID: "UC2", Handle: "@b"},
		},
	}

	if err := newLiveScanner(fetch, lib).ScanLive(context.Background()); err != nil {
		t.Fatalf("one channel's failure ended the pass: %v", err)
	}
	if len(fetch.listedURLs) != 2 {
		t.Errorf("asked %d channels, want both", len(fetch.listedURLs))
	}
}
