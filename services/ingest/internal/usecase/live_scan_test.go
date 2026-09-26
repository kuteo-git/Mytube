package usecase

import (
	"context"
	"errors"
	"fmt"
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

// recheckDownloader answers Preview per URL, and can refuse.
//
// deepenDownloader's own Preview never errors, and the failure path is half of
// what the recheck has to get right: a video upstream will not describe is the
// one thing that can wedge a queue ordered by "oldest claim first".
type recheckDownloader struct {
	streamsDownloader
	previews map[string]domain.ExternalVideo
	refuse   map[string]bool
	asked    []string
}

func (d *recheckDownloader) Preview(_ context.Context, url string) (domain.ExternalVideo, error) {
	d.asked = append(d.asked, url)
	if d.refuse[url] {
		return domain.ExternalVideo{}, errors.New("members-only")
	}
	return d.previews[url], nil
}

func watchURL(videoID string) string {
	return "https://www.youtube.com/watch?v=" + videoID
}

func newRecheckScanner(fetch domain.Downloader, lib domain.Library) *Scanner {
	s := newLiveScanner(fetch, lib)
	// The real gap is four seconds a video, and the quota is twenty.
	s.liveRecheckDelay = time.Microsecond
	return s
}

// The reported bug, at the seam it happened.
//
// VsQWkHo_E4o was on air with 34 concurrent viewers while its row said it had
// last been seen live fourteen hours earlier — so every list that cuts at thirty
// minutes hid it, and the channel's /streams tab lists nothing, so no pass was
// ever going to say otherwise.
func TestLiveRecheckWindsTheClockOnABroadcastStillRunning(t *testing.T) {
	fetch := &recheckDownloader{previews: map[string]domain.ExternalVideo{
		watchURL("VsQWkHo_E4o"): {ID: "VsQWkHo_E4o", Title: "station", LiveStatus: "is_live"},
	}}
	lib := &recordingLibrary{
		known:     map[string]bool{},
		channels:  map[string]domain.ExternalVideo{},
		staleLive: []string{"VsQWkHo_E4o"},
	}

	newRecheckScanner(fetch, lib).recheckKnownLive(context.Background())

	if len(fetch.asked) != 1 || fetch.asked[0] != watchURL("VsQWkHo_E4o") {
		t.Fatalf("asked %v, want one probe of the reported video", fetch.asked)
	}
	// The write is the whole point: live_checked_at moves only when an upsert
	// carries a live_status, so a pass that probed and wrote nothing would leave
	// the row exactly as stale as it found it.
	written, ok := lib.channels["VsQWkHo_E4o"]
	if !ok {
		t.Fatal("nothing was written back, so the row stays stale for ever")
	}
	if written.LiveStatus != "is_live" {
		t.Errorf("wrote live_status %q, want is_live", written.LiveStatus)
	}
}

// The other 568. A broadcast that ended keeps the ranker's exemption from the
// 365-day age filter for as long as its row says it is on air, and nothing ever
// said otherwise — the oldest such row was last checked on 22 August.
func TestLiveRecheckSettlesABroadcastThatHasEnded(t *testing.T) {
	fetch := &recheckDownloader{previews: map[string]domain.ExternalVideo{
		watchURL("old1"): {ID: "old1", LiveStatus: "was_live"},
	}}
	lib := &recordingLibrary{
		known:     map[string]bool{},
		channels:  map[string]domain.ExternalVideo{},
		staleLive: []string{"old1"},
	}

	newRecheckScanner(fetch, lib).recheckKnownLive(context.Background())

	if got := lib.channels["old1"].LiveStatus; got != "was_live" {
		t.Errorf("wrote live_status %q, want was_live", got)
	}
}

// A row that can never be settled must not wedge the queue.
//
// ListStaleLive orders by oldest claim first, and a members-only or deleted
// video answers nothing — so its word never changes and it is handed back at the
// head of the queue for ever. Measured on the eight oldest rows in this library,
// one was members-only.
func TestLiveRecheckAsksAnUndescribableVideoOnce(t *testing.T) {
	fetch := &recheckDownloader{
		previews: map[string]domain.ExternalVideo{
			watchURL("ok"): {ID: "ok", LiveStatus: "was_live"},
		},
		refuse: map[string]bool{watchURL("gone"): true},
	}
	lib := &recordingLibrary{
		known:     map[string]bool{},
		channels:  map[string]domain.ExternalVideo{},
		staleLive: []string{"gone", "ok"},
	}
	scanner := newRecheckScanner(fetch, lib)

	scanner.recheckKnownLive(context.Background())
	scanner.recheckKnownLive(context.Background())

	var goneAsked int
	for _, url := range fetch.asked {
		if url == watchURL("gone") {
			goneAsked++
		}
	}
	if goneAsked != 1 {
		t.Errorf("asked about the undescribable video %d times, want 1: %v", goneAsked, fetch.asked)
	}
	// And the row behind it is still reached, on both passes.
	if lib.channels["ok"].LiveStatus != "was_live" {
		t.Error("the row behind the failure was never settled")
	}
}

// Silence is not an answer, and the upsert reads it as one: an ExternalVideo
// carrying no live_status leaves both columns alone, so writing it would cost a
// request and leave the row in the set for ever.
func TestLiveRecheckWritesNothingWhenUpstreamNamesNoStatus(t *testing.T) {
	fetch := &recheckDownloader{previews: map[string]domain.ExternalVideo{
		watchURL("quiet"): {ID: "quiet", Title: "no word about liveness"},
	}}
	lib := &recordingLibrary{
		known:     map[string]bool{},
		channels:  map[string]domain.ExternalVideo{},
		staleLive: []string{"quiet"},
	}
	scanner := newRecheckScanner(fetch, lib)

	scanner.recheckKnownLive(context.Background())
	if len(lib.added) != 0 {
		t.Errorf("wrote %v, want nothing written", lib.added)
	}

	// And it is not asked again, for the same reason a refusal is not: there is
	// nothing to write, so there is nothing to ask about.
	scanner.recheckKnownLive(context.Background())
	if len(fetch.asked) != 1 {
		t.Errorf("asked %d times, want 1: %v", len(fetch.asked), fetch.asked)
	}
}

// The quota is what keeps this inside §8 risk 6. One probe is a full metadata
// request, and the backlog is hundreds of rows.
func TestLiveRecheckProbesNoMoreThanItsQuota(t *testing.T) {
	fetch := &recheckDownloader{previews: map[string]domain.ExternalVideo{}}
	lib := &recordingLibrary{known: map[string]bool{}, channels: map[string]domain.ExternalVideo{}}
	for i := 0; i < liveRecheckPerPass*3; i++ {
		id := fmt.Sprintf("v%d", i)
		lib.staleLive = append(lib.staleLive, id)
		fetch.previews[watchURL(id)] = domain.ExternalVideo{ID: id, LiveStatus: "was_live"}
	}

	newRecheckScanner(fetch, lib).recheckKnownLive(context.Background())

	if len(fetch.asked) != liveRecheckPerPass {
		t.Errorf("probed %d, want the quota of %d", len(fetch.asked), liveRecheckPerPass)
	}
}

// A rate-limit block presents as every request failing in a row, and pushing
// through it only lengthens the block.
func TestLiveRecheckStopsAfterConsecutiveFailures(t *testing.T) {
	fetch := &recheckDownloader{refuse: map[string]bool{}}
	lib := &recordingLibrary{known: map[string]bool{}, channels: map[string]domain.ExternalVideo{}}
	for i := 0; i < liveRecheckPerPass; i++ {
		id := fmt.Sprintf("v%d", i)
		lib.staleLive = append(lib.staleLive, id)
		fetch.refuse[watchURL(id)] = true
	}

	newRecheckScanner(fetch, lib).recheckKnownLive(context.Background())

	if len(fetch.asked) != liveRecheckFailureCutoff {
		t.Errorf("probed %d, want to stop at the cutoff of %d",
			len(fetch.asked), liveRecheckFailureCutoff)
	}
}
