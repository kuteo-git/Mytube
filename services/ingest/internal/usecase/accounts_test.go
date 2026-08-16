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

// A store that keeps everything in memory, so a test can say what state an
// account is in without writing files.
type memAccounts struct {
	accounts map[string]domain.Account
	paths    map[string]string
	recorded []struct {
		user   string
		result string
		failed bool
	}
}

func newMemAccounts(users ...string) *memAccounts {
	m := &memAccounts{accounts: map[string]domain.Account{}, paths: map[string]string{}}
	for _, u := range users {
		m.accounts[u] = domain.Account{UserID: u, State: domain.AccountOK}
		m.paths[u] = "/tmp/" + u + ".txt"
	}
	return m
}

func (m *memAccounts) Save(context.Context, string, string, string) error { return nil }
func (m *memAccounts) Remove(context.Context, string) error               { return nil }

func (m *memAccounts) List(context.Context) ([]domain.Account, error) {
	out := make([]domain.Account, 0, len(m.accounts))
	for _, a := range m.accounts {
		out = append(out, a)
	}
	return out, nil
}

func (m *memAccounts) CookiePath(_ context.Context, userID string) (string, error) {
	if a, ok := m.accounts[userID]; !ok || a.State == domain.AccountExpired {
		return "", domain.ErrNoAccount
	}
	return m.paths[userID], nil
}

func (m *memAccounts) Record(_ context.Context, userID, result string, failed bool) error {
	m.recorded = append(m.recorded, struct {
		user   string
		result string
		failed bool
	}{userID, result, failed})
	if failed {
		a := m.accounts[userID]
		a.Failures++
		if a.Failures >= domain.AccountFailureLimit {
			a.State = domain.AccountExpired
		}
		m.accounts[userID] = a
	}
	return nil
}

// A feed source that answers per feed, and records which session asked.
type memFeeds struct {
	byFeed map[string][]domain.ExternalVideo
	authOn map[string]bool
	asked  []struct{ cookies, feed string }
	// The subscription list, which is a list of channels rather than of the
	// uploads of those channels. See domain.FeedChannels.
	channels []domain.AccountChannel
	// The member's own playlists, by name. Their contents come from byFeed,
	// keyed by the playlist URL, because that is how they are really read.
	playlists []domain.AccountPlaylist
	// Feeds that fail, by name or URL, with the error upstream really gives.
	failFeed map[string]error
}

func (m *memFeeds) ListAccountPlaylists(
	_ context.Context, cookiesFile string,
) ([]domain.AccountPlaylist, error) {
	m.asked = append(m.asked, struct{ cookies, feed string }{cookiesFile, domain.FeedPlaylists})
	if m.authOn[domain.FeedPlaylists] {
		return nil, domain.ErrAccountAuth
	}
	return m.playlists, nil
}

func (m *memFeeds) ListAccountChannels(
	_ context.Context, cookiesFile string,
) ([]domain.AccountChannel, error) {
	m.asked = append(m.asked, struct{ cookies, feed string }{cookiesFile, domain.FeedChannels})
	if m.authOn[domain.FeedChannels] {
		return nil, domain.ErrAccountAuth
	}
	return m.channels, nil
}

func (m *memFeeds) ListAccountFeed(
	_ context.Context, cookiesFile, feed string, _ int32,
) ([]domain.ExternalVideo, error) {
	m.asked = append(m.asked, struct{ cookies, feed string }{cookiesFile, feed})
	if m.authOn[feed] {
		return nil, domain.ErrAccountAuth
	}
	if err := m.failFeed[feed]; err != nil {
		return nil, err
	}
	return m.byFeed[feed], nil
}

func video(id, channel string) domain.ExternalVideo {
	return domain.ExternalVideo{
		ID:        id,
		ChannelID: channel,
		SourceURL: "https://www.youtube.com/watch?v=" + id,
	}
}

// memSignals is the ranker's own record, which is a different record from the
// catalogue's and has to be written too.
type memSignals struct {
	subscribed []struct{ userID, channelID string }
	liked      []struct{ userID, videoID string }
}

func (m *memSignals) Subscribed(_ context.Context, userID, channelID string, _ time.Time) error {
	m.subscribed = append(m.subscribed, struct{ userID, channelID string }{userID, channelID})
	return nil
}

func (m *memSignals) Liked(_ context.Context, userID, videoID string, _ time.Time) error {
	m.liked = append(m.liked, struct{ userID, videoID string }{userID, videoID})
	return nil
}

func newAccountScanner(t *testing.T, accounts *memAccounts, feeds *memFeeds, lib *recordingLibrary) *AccountScanner {
	t.Helper()
	return newAccountScannerWith(t, accounts, feeds, lib, &memSignals{})
}

func newAccountScannerWith(
	t *testing.T, accounts *memAccounts, feeds *memFeeds, lib *recordingLibrary, signals domain.SignalSink,
) *AccountScanner {
	t.Helper()
	s := NewAccountScanner(accounts, feeds, lib, signals, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.pause = time.Nanosecond
	return s
}

func TestSubscriptionsLandUnderTheMemberWhoseTheyAre(t *testing.T) {
	accounts := newMemAccounts("u_luc", "u_vo")
	feeds := &memFeeds{
		byFeed:   map[string][]domain.ExternalVideo{},
		channels: []domain.AccountChannel{{ID: "ch_1", Name: "One"}},
	}
	lib := &recordingLibrary{known: map[string]bool{}}

	if _, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), ""); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	// One channel, two members: each has to be subscribed on their own behalf.
	users := map[string]bool{}
	for _, s := range lib.subscribedBy {
		users[s.userID] = true
		if s.channelID != "ch_1" {
			t.Errorf("subscribed to %q", s.channelID)
		}
	}
	if !users["u_luc"] || !users["u_vo"] {
		t.Errorf("subscriptions landed on %v, want both members", users)
	}
}

// Each member's feeds are read with their own session.
func TestEachMemberIsReadAsThemselves(t *testing.T) {
	accounts := newMemAccounts("u_luc", "u_vo")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{}}
	lib := &recordingLibrary{known: map[string]bool{}}

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), "")

	seen := map[string]bool{}
	for _, a := range feeds.asked {
		seen[a.cookies] = true
	}
	if !seen["/tmp/u_luc.txt"] || !seen["/tmp/u_vo.txt"] {
		t.Errorf("sessions used: %v, want one per member", seen)
	}
}

// YouTube's own recommendations come in as material, never as ranking.
//
// §6's whole claim is that every score can be explained; YouTube's ordering
// cannot be. So it is tagged, and recsys allows the tag in the discovery bucket
// only — the same fence RELATED already sits behind.
func TestRecommendationsAreTaggedSoRankingCanFenceThem(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{
		domain.FeedSubscriptions: {video("chosen", "ch_1")},
		domain.FeedRecommended:   {video("guessed", "ch_2")},
	}}
	lib := &recordingLibrary{known: map[string]bool{}, channels: map[string]domain.ExternalVideo{}}

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), "")

	if got := lib.channels["chosen"].DiscoveredVia; got != "SOURCE" {
		t.Errorf("a subscription video was tagged %q, want SOURCE", got)
	}
	if got := lib.channels["guessed"].DiscoveredVia; got != "YOUTUBE_REC" {
		t.Errorf("a recommendation was tagged %q, want YOUTUBE_REC", got)
	}
}

// Liked videos on YouTube become likes here, for that member alone.
func TestLikesAreImportedForThatMemberOnly(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{
		domain.FeedLiked: {video("loved", "ch_1")},
	}}
	lib := &recordingLibrary{known: map[string]bool{}}

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), "")

	if len(lib.liked) != 1 || lib.liked[0].userID != "u_luc" || lib.liked[0].videoID != "loved" {
		t.Errorf("likes = %+v", lib.liked)
	}
}

// A dead session stops the pass for that member at once.
//
// Four more refused requests teach nothing and are four more marks against an
// account that is already in trouble.
func TestADeadSessionStopsThatMembersPassImmediately(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	feeds := &memFeeds{
		byFeed: map[string][]domain.ExternalVideo{},
		authOn: map[string]bool{domain.FeedChannels: true},
	}
	lib := &recordingLibrary{known: map[string]bool{}}

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), "")

	if len(feeds.asked) != 1 {
		t.Errorf("made %d requests after being signed out, want 1", len(feeds.asked))
	}
	if len(accounts.recorded) != 1 || !accounts.recorded[0].failed {
		t.Errorf("the failure was not recorded: %+v", accounts.recorded)
	}
}

// An expired account is skipped entirely, not retried hourly.
func TestAnExpiredAccountIsNotAskedAgain(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	a := accounts.accounts["u_luc"]
	a.State = domain.AccountExpired
	accounts.accounts["u_luc"] = a

	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{}}
	lib := &recordingLibrary{known: map[string]bool{}}

	result, _ := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), "")

	if len(feeds.asked) != 0 {
		t.Errorf("an expired session was replayed: %v", feeds.asked)
	}
	if result.Expired != 1 {
		t.Errorf("expired = %d, want 1", result.Expired)
	}
}

// An ordinary failure is not a dead session, and must not retire the account.
//
// googlevideo refuses in waves — the same URL has answered 206, then 403, then
// 206 within an hour here. Reading that as "you are signed out" would take a
// working account out of service on a bad afternoon.
func TestAnOrdinaryFailureDoesNotRetireTheAccount(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{}}
	feeds.byFeed[domain.FeedSubscriptions] = nil
	lib := &recordingLibrary{known: map[string]bool{}}

	scanner := newAccountScanner(t, accounts, feeds, lib)
	// A non-auth error on every feed.
	feeds.authOn = nil
	failing := &failingFeeds{err: errors.New("network unreachable")}
	scanner.feeds = failing

	_, _ = scanner.ScanAll(context.Background(), "")

	if len(accounts.recorded) != 1 || accounts.recorded[0].failed {
		t.Errorf("an ordinary error was recorded as an authentication failure: %+v", accounts.recorded)
	}
	if accounts.accounts["u_luc"].State == domain.AccountExpired {
		t.Error("a network error expired the account")
	}
}

type failingFeeds struct{ err error }

func (f *failingFeeds) ListAccountFeed(
	context.Context, string, string, int32,
) ([]domain.ExternalVideo, error) {
	return nil, f.err
}

func (f *failingFeeds) ListAccountPlaylists(
	context.Context, string,
) ([]domain.AccountPlaylist, error) {
	return nil, f.err
}

func (f *failingFeeds) ListAccountChannels(
	context.Context, string,
) ([]domain.AccountChannel, error) {
	return nil, f.err
}

// Nothing about the session reaches the note shown on the settings screen.
func TestTheRecordedNoteHoldsNoSession(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{
		domain.FeedSubscriptions: {video("a", "ch_1")},
	}}
	lib := &recordingLibrary{known: map[string]bool{}}

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), "")

	if len(accounts.recorded) != 1 {
		t.Fatalf("recorded = %+v", accounts.recorded)
	}
	note := accounts.recorded[0].result
	for _, secret := range []string{"/tmp/u_luc.txt", "SID", "cookie"} {
		if contains(note, secret) {
			t.Errorf("the note %q mentions %q", note, secret)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// The ranker keeps its own record, and it has to be written too.
//
// This is the bug the whole file exists around. `catalog.subscriptions` is the
// authoritative list and is what the Subscriptions page shows; `recsys.signals`
// is what ranking reads. Writing only the first left an imported account
// looking as though it followed nobody — measured on a real household member
// with 19 imported subscriptions and 829 videos from them, whose entire first
// page came back labelled DISCOVERY, 24 of 24.
func TestAnImportedSubscriptionReachesTheRankerToo(t *testing.T) {
	accounts := newMemAccounts("u_lm")
	feeds := &memFeeds{
		byFeed:   map[string][]domain.ExternalVideo{domain.FeedLiked: {video("b", "ch_2")}},
		channels: []domain.AccountChannel{{ID: "ch_1"}},
	}
	lib := &recordingLibrary{known: map[string]bool{}}
	signals := &memSignals{}

	if _, err := newAccountScannerWith(t, accounts, feeds, lib, signals).ScanAll(context.Background(), ""); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	if len(signals.subscribed) != 1 || signals.subscribed[0].channelID != "ch_1" {
		t.Errorf("the ranker was not told about the subscription: %+v", signals.subscribed)
	}
	if signals.subscribed[0].userID != "u_lm" {
		t.Errorf("told the ranker the wrong member: %+v", signals.subscribed)
	}
	if len(signals.liked) != 1 || signals.liked[0].videoID != "b" {
		t.Errorf("the ranker was not told about the like: %+v", signals.liked)
	}
}

// A lost signal degrades ranking; it must never undo an import that has already
// written the authoritative row.
func TestAFailingRankerDoesNotFailTheImport(t *testing.T) {
	accounts := newMemAccounts("u_lm")
	feeds := &memFeeds{
		byFeed:   map[string][]domain.ExternalVideo{},
		channels: []domain.AccountChannel{{ID: "ch_1"}},
	}
	lib := &recordingLibrary{known: map[string]bool{}}

	result, err := newAccountScannerWith(t, accounts, feeds, lib, failingSignals{}).ScanAll(context.Background(), "")
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if result.Subscriptions != 1 {
		t.Errorf("subscriptions = %d, want the catalogue write to stand", result.Subscriptions)
	}
	if len(lib.subscribedBy) != 1 {
		t.Error("the authoritative row was rolled back over a lost signal")
	}
}

type failingSignals struct{}

func (failingSignals) Subscribed(context.Context, string, string, time.Time) error {
	return errors.New("recsys is down")
}

func (failingSignals) Liked(context.Context, string, string, time.Time) error {
	return errors.New("recsys is down")
}

// Who a member follows comes from the subscription list, not from whoever
// happened to post lately.
//
// Measured on a real household member: 152 subscribed channels, of which the 50
// most recent uploads named 19 — so 133 channels could not be imported at all,
// and a channel that had gone quiet for a fortnight never could be.
func TestSubscriptionsComeFromTheListAndNotTheUploads(t *testing.T) {
	accounts := newMemAccounts("u_lm")
	feeds := &memFeeds{
		byFeed: map[string][]domain.ExternalVideo{
			// The uploads feed carries one channel, busily.
			domain.FeedSubscriptions: {video("a", "ch_busy"), video("b", "ch_busy")},
		},
		// The list carries that one and a channel that has posted nothing.
		channels: []domain.AccountChannel{{ID: "ch_busy"}, {ID: "ch_quiet"}},
	}
	lib := &recordingLibrary{known: map[string]bool{}}

	result, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), "")
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	got := map[string]bool{}
	for _, s := range lib.subscribedBy {
		got[s.channelID] = true
	}
	if !got["ch_quiet"] {
		t.Error("a channel that has not posted recently was not imported")
	}
	// Counted per channel, not per video. The count used to be incremented once
	// per upload, so a scan finding 19 channels reported 49 subscriptions.
	if result.Subscriptions != 2 {
		t.Errorf("subscriptions = %d, want 2 channels", result.Subscriptions)
	}
}

// A short list, or none at all, must not unsubscribe anybody.
//
// Ranking reads this record, so a bad minute upstream would otherwise empty a
// member's Home feed with no trace of why. Unsubscribing stays something done
// in this app.
func TestAnEmptyListUnsubscribesNobody(t *testing.T) {
	accounts := newMemAccounts("u_lm")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{}, channels: nil}
	lib := &recordingLibrary{known: map[string]bool{}}

	if _, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), ""); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	// Nothing at all is written. The import only ever adds, so an empty answer
	// is an empty answer rather than an instruction to clear the list — which is
	// what reconciling against it would have made it.
	if len(lib.subscribedBy) != 0 {
		t.Errorf("an empty list wrote %d subscription changes", len(lib.subscribedBy))
	}
}

// A Watch Later video lands on the member's Watch Later list, not merely in the
// library.
//
// The feed was read from the day the importer was written, but its videos were
// stored as ordinary ones — so a list somebody had built deliberately arrived as
// an anonymous handful of new videos, and catalog.watch_later stayed empty for
// everybody while every video read reported in_watch_later: false.
func TestWatchLaterLandsOnTheList(t *testing.T) {
	accounts := newMemAccounts("u_lm")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{
		domain.FeedWatchLater: {video("later", "ch_1")},
		domain.FeedLiked:      {video("loved", "ch_2")},
	}}
	lib := &recordingLibrary{known: map[string]bool{}}

	if _, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), ""); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	// Written once, from the whole read: the list is a mirror of YouTube's
	// rather than an accumulation, so it is set in one go or not at all.
	if len(lib.watchLater) != 1 || lib.watchLater[0].userID != "u_lm" ||
		len(lib.watchLater[0].videoIDs) != 1 || lib.watchLater[0].videoIDs[0] != "later" {
		t.Errorf("watch later: %+v", lib.watchLater)
	}
	// A read below the cap is the whole list, so the mirror may remove.
	if !lib.watchLater[0].complete {
		t.Error("a short read was not treated as the whole list")
	}
	// And only that feed's. A liked video is not a note about what to watch next.
	for _, id := range lib.watchLater[0].videoIDs {
		if id == "loved" {
			t.Error("a liked video was put on Watch Later")
		}
	}
}

// Watch Later and Liked videos appear in YouTube's playlist list, and neither is
// a playlist here.
//
// Both already arrive through their own feeds, and Watch Later is deliberately
// not a playlist at all — it has no name, cannot be created and cannot be
// deleted. Importing them would put two rows on the playlists page that mean the
// same as two pages already in the sidebar.
func TestWatchLaterAndLikedAreNotImportedAsPlaylists(t *testing.T) {
	for _, id := range []string{"WL", "LL"} {
		if domain.IsImportablePlaylist(id) {
			t.Errorf("%s would be imported as a playlist", id)
		}
	}
	if !domain.IsImportablePlaylist("PLM8mlc5hM62hfq9WsRCfkiCyFFSHVdLbp") {
		t.Error("an ordinary playlist was refused")
	}
}

// Every playlist is named each pass; the ones never read are read in full, and
// only a few already-read ones are refreshed.
//
// The split is the whole of what keeps this affordable *and* what stops a
// freshly imported playlist sitting empty for hours. Filling the library happens
// once — 28 requests three seconds apart is shorter and slower than an ordinary
// hour of the anonymous scanner. Re-reading is the part that repeats hourly, and
// that is where the small number belongs: §8's risk 6, raised from an address to
// an account.
func TestUnreadPlaylistsAreAllReadAndRereadsAreFew(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	lists := make([]domain.AccountPlaylist, 0, 30)
	unread := make([]domain.StalePlaylist, 0, 30)
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("PL%d", i)
		lists = append(lists, domain.AccountPlaylist{ID: id, Title: id})
		unread = append(unread, domain.StalePlaylist{
			ID: id, UserID: "u_luc", SourceURL: domain.PlaylistURL(id),
		})
	}
	// Ten that have been read before, so the re-read budget has something to
	// spend and can be told apart from the first fill.
	stale := make([]domain.StalePlaylist, 0, 10)
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("OLD%d", i)
		stale = append(stale, domain.StalePlaylist{
			ID: id, UserID: "u_luc", SourceURL: domain.PlaylistURL(id),
		})
	}
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{}, playlists: lists}
	lib := &recordingLibrary{
		known:           map[string]bool{},
		unreadPlaylists: unread,
		stalePlaylists:  stale,
	}

	result, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), "")
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	if result.Playlists != 30 {
		t.Errorf("named %d playlists, want all 30", result.Playlists)
	}
	// Both bounds have to be in the request. Asking for everything and trimming
	// afterwards would mean catalog handed over the rows and the pass read them.
	if lib.unreadLimit != firstFillLimit {
		t.Errorf("asked for %d unread playlists, want %d", lib.unreadLimit, firstFillLimit)
	}
	if lib.staleLimit != playlistRereadsPerPass {
		t.Errorf("asked for %d stale playlists, want %d", lib.staleLimit, playlistRereadsPerPass)
	}
	if want := 30 + playlistRereadsPerPass; len(lib.playlistItems) != want {
		t.Errorf("read %d playlists in one pass, want %d", len(lib.playlistItems), want)
	}
}

// And the first fill is bounded, so a member with hundreds does not do hundreds
// in one go.
func TestTheFirstFillIsStillBounded(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	unread := make([]domain.StalePlaylist, 0, firstFillLimit*2)
	for i := 0; i < firstFillLimit*2; i++ {
		id := fmt.Sprintf("PL%d", i)
		unread = append(unread, domain.StalePlaylist{
			ID: id, UserID: "u_luc", SourceURL: domain.PlaylistURL(id),
		})
	}
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{}}
	lib := &recordingLibrary{known: map[string]bool{}, unreadPlaylists: unread}

	if _, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), ""); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if len(lib.playlistItems) > firstFillLimit {
		t.Errorf("read %d playlists, want at most %d", len(lib.playlistItems), firstFillLimit)
	}
}

// The button scans whoever pressed it. The hourly timer scans everybody.
//
// It sits on a screen about *your* account, so scanning the whole household
// from it is a surprise — and on a house of five, four times the requests
// nobody asked for.
func TestScanningOneMemberLeavesTheOthersAlone(t *testing.T) {
	accounts := newMemAccounts("u_luc", "u_lm")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{}}
	lib := &recordingLibrary{known: map[string]bool{}}

	if _, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), "u_luc"); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	for _, a := range feeds.asked {
		if a.cookies == "/tmp/u_lm.txt" {
			t.Error("scanning one member reached another member's session")
		}
	}
}

// A playlist upstream lists but will not hand over is asked once, not for ever.
//
// Measured: 10 of this household's 27 playlists answer "The playlist does not
// exist" when fetched by URL, reproducibly, with a live session. Without
// recording that, each costs a request every pass and sits at the front of the
// unread queue ahead of playlists that could have been read.
func TestAPlaylistUpstreamRefusesIsAskedOnce(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	feeds := &memFeeds{
		byFeed: map[string][]domain.ExternalVideo{},
		// The read of this playlist fails the way YouTube really fails it.
		failFeed: map[string]error{
			domain.PlaylistURL("PLgone"): errors.New(
				"[youtube:tab] PLgone: YouTube said: The playlist does not exist."),
		},
	}
	lib := &recordingLibrary{
		known: map[string]bool{},
		unreadPlaylists: []domain.StalePlaylist{
			{ID: "pl_gone", UserID: "u_luc", SourceURL: domain.PlaylistURL("PLgone")},
		},
	}

	if _, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), ""); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	if len(lib.unavailablePlaylists) != 1 || lib.unavailablePlaylists[0] != "pl_gone" {
		t.Errorf("upstream's refusal was not recorded: %v", lib.unavailablePlaylists)
	}
}

// And an ordinary failure is not read as one. A network error says nothing
// about the playlist, and recording it as a refusal would bury a readable list.
func TestAnOrdinaryFailureDoesNotBuryAPlaylist(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	feeds := &memFeeds{
		byFeed:   map[string][]domain.ExternalVideo{},
		failFeed: map[string]error{domain.PlaylistURL("PLfine"): errors.New("network unreachable")},
	}
	lib := &recordingLibrary{
		known: map[string]bool{},
		unreadPlaylists: []domain.StalePlaylist{
			{ID: "pl_fine", UserID: "u_luc", SourceURL: domain.PlaylistURL("PLfine")},
		},
	}

	if _, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background(), ""); err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if len(lib.unavailablePlaylists) != 0 {
		t.Errorf("a network error buried a playlist: %v", lib.unavailablePlaylists)
	}
}
