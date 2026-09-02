package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// A fixed now, because every assertion here is about an edge measured against
// it. `time.Now()` inside the ranker and inside the test are not the same
// instant, and a video built to sit one minute inside the window would fall out
// of it on a slow machine.
func missedRanker(features []domain.VideoFeatures, profile domain.UserProfile, now time.Time) *Ranker {
	r := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	r.now = func() time.Time { return now }
	return r
}

func missedProfile(subscribed []string, watched map[string]float32) domain.UserProfile {
	p := emptyProfile()
	for _, id := range subscribed {
		p.Subscribed[id] = true
	}
	for id, f := range watched {
		p.WatchedFraction[id] = f
	}
	return p
}

func missedIDs(t *testing.T, page MissedPage) []string {
	t.Helper()
	out := make([]string, 0, len(page.Videos))
	for _, v := range page.Videos {
		out = append(out, v.VideoID)
	}
	return out
}

// The window is a hard edge, and both sides of it are asserted.
//
// A penalty would be wrong here in a way it is not wrong in the feed: this list
// is a claim that everything on it went up today, and a video from Tuesday
// sitting at the bottom is that claim being false quietly.
func TestMissedKeepsTheWindow(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "just-inside", ChannelID: "c", PublishedAt: now.Add(-23*time.Hour - 59*time.Minute)},
		{VideoID: "just-outside", ChannelID: "c", PublishedAt: now.Add(-24*time.Hour - 1*time.Minute)},
	}
	r := missedRanker(features, missedProfile([]string{"c"}, nil), now)

	page, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := missedIDs(t, page)
	if len(got) != 1 || got[0] != "just-inside" {
		t.Errorf("got %v, want [just-inside]", got)
	}
}

// Ten per cent, not ninety-five.
//
// The ranker's own isWatched asks whether somebody finished something. This
// asks whether they have dealt with it, and opening a video for a few seconds
// is not dealing with it — that is the tap this threshold is set below.
func TestMissedHidesWhatWasWatchedAndKeepsWhatWasGlanced(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "glanced", ChannelID: "c", PublishedAt: now.Add(-time.Hour)},
		{VideoID: "watched", ChannelID: "c", PublishedAt: now.Add(-time.Hour)},
	}
	profile := missedProfile([]string{"c"}, map[string]float32{
		"glanced": 0.09,
		"watched": 0.11,
	})
	r := missedRanker(features, profile, now)

	page, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := missedIDs(t, page)
	if len(got) != 1 || got[0] != "glanced" {
		t.Errorf("got %v, want [glanced]", got)
	}
}

// However popular. This list is about the channels this household chose, and a
// viral video from a channel nobody follows is what Home's discovery share is
// for.
func TestMissedIgnoresChannelsNobodyFollows(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "followed", ChannelID: "mine", PublishedAt: now.Add(-time.Hour), ViewCount: 10},
		{VideoID: "viral", ChannelID: "theirs", PublishedAt: now.Add(-time.Hour), ViewCount: 9_000_000},
	}
	r := missedRanker(features, missedProfile([]string{"mine"}, nil), now)

	page, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := missedIDs(t, page)
	if len(got) != 1 || got[0] != "followed" {
		t.Errorf("got %v, want [followed]", got)
	}
}

// Affinity first, views second — the priority the request stated.
//
// Both channels are followed and the one this viewer never watches has three
// orders of magnitude more views, so views alone would put it first.
func TestMissedPutsAWatchedChannelAboveABusierOne(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "old-favourite", ChannelID: "watched", PublishedAt: now.Add(-40 * 24 * time.Hour), DurationSeconds: 600},
		{VideoID: "from-favourite", ChannelID: "watched", PublishedAt: now.Add(-time.Hour), ViewCount: 500},
		{VideoID: "from-stranger", ChannelID: "followed", PublishedAt: now.Add(-time.Hour), ViewCount: 500_000},
	}
	profile := missedProfile(
		[]string{"watched", "followed"},
		map[string]float32{"old-favourite": 1},
	)
	profile.WatchedAt = map[string]time.Time{"old-favourite": now.Add(-time.Hour)}
	r := missedRanker(features, profile, now)

	page, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := missedIDs(t, page)
	if len(got) != 2 || got[0] != "from-favourite" {
		t.Errorf("got %v, want from-favourite first", got)
	}
}

// Every followed channel that posted gets a row before anyone gets a second.
//
// Measured against the running library before the round-robin existed: three
// channels held 97 of 100 rows, and a channel with one upload sat below forty
// news clips. Ordering alone cannot fix that — the channels flooding the page
// are the ones genuinely watched most — so the fix is that no channel is allowed
// a run.
func TestMissedGivesEveryChannelARowBeforeAnySecond(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "news1", ChannelID: "news", PublishedAt: now.Add(-time.Hour), ViewCount: 900_000},
		{VideoID: "news2", ChannelID: "news", PublishedAt: now.Add(-time.Hour), ViewCount: 800_000},
		{VideoID: "news3", ChannelID: "news", PublishedAt: now.Add(-time.Hour), ViewCount: 700_000},
		{VideoID: "only", ChannelID: "small", PublishedAt: now.Add(-time.Hour), ViewCount: 40},
	}
	r := missedRanker(features, missedProfile([]string{"news", "small"}, nil), now)

	page, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := missedIDs(t, page)
	if len(got) != 4 {
		t.Fatalf("got %v, want all four", got)
	}
	if got[0] != "news1" || got[1] != "only" {
		t.Errorf("got %v, want the small channel's single upload second", got)
	}
}

// A Short is not something anybody misses, however popular.
//
// Read from catalog's flag, never from the duration: the feed's own check
// records the measurement — 14- and 9-second videos are ordinary clips while
// 40- and 59-second ones are Shorts.
func TestMissedLeavesOutShorts(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "upload", ChannelID: "c", PublishedAt: now.Add(-time.Hour), ViewCount: 10},
		{
			VideoID: "short", ChannelID: "c", PublishedAt: now.Add(-time.Hour),
			ViewCount: 2_000_000, DurationSeconds: 45, IsShort: true,
		},
		// The other half of the rule: a short *video* that YouTube does not call
		// a Short stays, so nothing here is deciding by length.
		{
			VideoID: "brief", ChannelID: "c", PublishedAt: now.Add(-time.Hour),
			ViewCount: 20, DurationSeconds: 9,
		},
	}
	r := missedRanker(features, missedProfile([]string{"c"}, nil), now)

	page, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := missedIDs(t, page)
	if len(got) != 2 {
		t.Fatalf("got %v, want the two that are not Shorts", got)
	}
	for _, id := range got {
		if id == "short" {
			t.Errorf("a Short reached the list: %v", got)
		}
	}
}

// Among channels watched equally, the busier video leads.
func TestMissedBreaksAffinityTiesOnViews(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "quiet", ChannelID: "a", PublishedAt: now.Add(-time.Hour), ViewCount: 100},
		{VideoID: "busy", ChannelID: "b", PublishedAt: now.Add(-time.Hour), ViewCount: 100_000},
	}
	r := missedRanker(features, missedProfile([]string{"a", "b"}, nil), now)

	page, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := missedIDs(t, page)
	if len(got) != 2 || got[0] != "busy" {
		t.Errorf("got %v, want busy first", got)
	}
}

// A household following nothing gets an empty list and no error. That is the
// answer, and it is what makes the chip disappear rather than lead to a blank
// page.
func TestMissedIsEmptyWithNoSubscriptions(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "a", ChannelID: "c", PublishedAt: now.Add(-time.Hour)},
	}
	r := missedRanker(features, emptyProfile(), now)

	page, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Videos) != 0 || page.Remaining != 0 {
		t.Errorf("got %d videos and %d remaining, want none", len(page.Videos), page.Remaining)
	}
}

// Pages do not overlap and do not skip, and the last one says nothing is left.
func TestMissedPagesWithoutRepeating(t *testing.T) {
	now := time.Now()
	features := make([]domain.VideoFeatures, 0, 5)
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		features = append(features, domain.VideoFeatures{
			VideoID:     id,
			ChannelID:   "c",
			PublishedAt: now.Add(-time.Hour),
			ViewCount:   int64(100 * (5 - i)),
		})
	}
	r := missedRanker(features, missedProfile([]string{"c"}, nil), now)

	first, err := r.GetMissed(context.Background(), "u", 0, 2, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.Remaining != 3 {
		t.Errorf("remaining after page one = %d, want 3", first.Remaining)
	}
	second, err := r.GetMissed(context.Background(), "u", 0, 2, 2, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	last, err := r.GetMissed(context.Background(), "u", 0, 2, 4, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if last.Remaining != 0 {
		t.Errorf("remaining after the last page = %d, want 0", last.Remaining)
	}

	seen := map[string]bool{}
	for _, page := range []MissedPage{first, second, last} {
		for _, v := range page.Videos {
			if seen[v.VideoID] {
				t.Errorf("%s appeared on two pages", v.VideoID)
			}
			seen[v.VideoID] = true
		}
	}
	if len(seen) != 5 {
		t.Errorf("saw %d videos across three pages, want 5", len(seen))
	}
}

// A window given by the caller wins over the default, which is what makes the
// number a setting rather than a constant with a comment.
func TestMissedTakesTheWindowItIsGiven(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "two-hours-old", ChannelID: "c", PublishedAt: now.Add(-2 * time.Hour)},
	}
	r := missedRanker(features, missedProfile([]string{"c"}, nil), now)

	wide, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := r.GetMissed(context.Background(), "u", time.Hour, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(wide.Videos) != 1 {
		t.Errorf("the default day should hold a two-hour-old video, got %d", len(wide.Videos))
	}
	if len(narrow.Videos) != 0 {
		t.Errorf("an hour-wide window should not, got %d", len(narrow.Videos))
	}
}

// A refresh leads with a different channel, and every channel still gets a row
// before anybody gets a second.
//
// This is the whole of what the rotation is for. Without it a pull-to-refresh
// answered with the identical list in the identical order — correct, and useless
// as a gesture.
func TestMissedRotatesWhichChannelOpensTheList(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "a1", ChannelID: "a", PublishedAt: now.Add(-time.Hour), ViewCount: 300},
		{VideoID: "b1", ChannelID: "b", PublishedAt: now.Add(-time.Hour), ViewCount: 200},
		{VideoID: "c1", ChannelID: "c", PublishedAt: now.Add(-time.Hour), ViewCount: 100},
	}
	r := missedRanker(features, missedProfile([]string{"a", "b", "c"}, nil), now)

	first, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	third, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 2)
	if err != nil {
		t.Fatal(err)
	}

	if got := missedIDs(t, first); got[0] != "a1" {
		t.Errorf("unrotated starts at %v, want a1", got)
	}
	if got := missedIDs(t, second); got[0] != "b1" {
		t.Errorf("rotated by one starts at %v, want b1", got)
	}
	if got := missedIDs(t, third); got[0] != "c1" {
		t.Errorf("rotated by two starts at %v, want c1", got)
	}
	// Same set every time. A rotation reorders; it must never drop or add.
	for _, page := range []MissedPage{first, second, third} {
		if len(page.Videos) != 3 {
			t.Errorf("got %d videos, want all three", len(page.Videos))
		}
	}
}

// A rotation past the number of channels wraps rather than emptying the list.
//
// The seed is a nanosecond clock reading, so it is always far larger than the
// handful of channels it is divided by — the modulo is the whole of the safety
// and it is asserted rather than assumed.
func TestMissedRotationWrapsAroundAHugeSeed(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "a1", ChannelID: "a", PublishedAt: now.Add(-time.Hour), ViewCount: 300},
		{VideoID: "b1", ChannelID: "b", PublishedAt: now.Add(-time.Hour), ViewCount: 200},
	}
	r := missedRanker(features, missedProfile([]string{"a", "b"}, nil), now)

	page, err := r.GetMissed(context.Background(), "u", 0, 24, 0, nil, 1_756_800_000_000_000_001)
	if err != nil {
		t.Fatal(err)
	}
	if got := missedIDs(t, page); len(got) != 2 || got[0] != "b1" {
		t.Errorf("got %v, want both videos starting at b1", got)
	}
}

// Paging holds while the rotation does. The token carries it precisely so that
// a reader who scrolls does not have the list reordered underneath them.
func TestMissedPagesAgreeUnderOneRotation(t *testing.T) {
	now := time.Now()
	features := make([]domain.VideoFeatures, 0, 4)
	for i, id := range []string{"a1", "b1", "c1", "d1"} {
		features = append(features, domain.VideoFeatures{
			VideoID:     id,
			ChannelID:   string(rune('a' + i)),
			PublishedAt: now.Add(-time.Hour),
			ViewCount:   int64(100 * (4 - i)),
		})
	}
	profile := missedProfile([]string{"a", "b", "c", "d"}, nil)
	r := missedRanker(features, profile, now)

	first, err := r.GetMissed(context.Background(), "u", 0, 2, 0, nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.GetMissed(context.Background(), "u", 0, 2, 2, nil, 3)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, page := range []MissedPage{first, second} {
		for _, v := range page.Videos {
			if seen[v.VideoID] {
				t.Errorf("%s appeared on both pages", v.VideoID)
			}
			seen[v.VideoID] = true
		}
	}
	if len(seen) != 4 {
		t.Errorf("saw %d of 4 videos across two pages", len(seen))
	}
}
