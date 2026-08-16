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
}

func (m *memFeeds) ListAccountFeed(
	_ context.Context, cookiesFile, feed string, _ int32,
) ([]domain.ExternalVideo, error) {
	m.asked = append(m.asked, struct{ cookies, feed string }{cookiesFile, feed})
	if m.authOn[feed] {
		return nil, domain.ErrAccountAuth
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

func newAccountScanner(t *testing.T, accounts *memAccounts, feeds *memFeeds, lib *recordingLibrary) *AccountScanner {
	t.Helper()
	s := NewAccountScanner(accounts, feeds, lib, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.pause = time.Nanosecond
	return s
}

func TestSubscriptionsLandUnderTheMemberWhoseTheyAre(t *testing.T) {
	accounts := newMemAccounts("u_luc", "u_vo")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{
		domain.FeedSubscriptions: {video("a", "ch_1")},
	}}
	lib := &recordingLibrary{known: map[string]bool{}}

	if _, err := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background()); err != nil {
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

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background())

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

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background())

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

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background())

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
		authOn: map[string]bool{domain.FeedSubscriptions: true},
	}
	lib := &recordingLibrary{known: map[string]bool{}}

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background())

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

	result, _ := newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background())

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

	_, _ = scanner.ScanAll(context.Background())

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

// Nothing about the session reaches the note shown on the settings screen.
func TestTheRecordedNoteHoldsNoSession(t *testing.T) {
	accounts := newMemAccounts("u_luc")
	feeds := &memFeeds{byFeed: map[string][]domain.ExternalVideo{
		domain.FeedSubscriptions: {video("a", "ch_1")},
	}}
	lib := &recordingLibrary{known: map[string]bool{}}

	_, _ = newAccountScanner(t, accounts, feeds, lib).ScanAll(context.Background())

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
