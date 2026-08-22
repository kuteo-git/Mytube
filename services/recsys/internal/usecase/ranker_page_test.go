package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

type stubFeatures struct{ features []domain.VideoFeatures }

func (s stubFeatures) ListVideoFeatures(context.Context) ([]domain.VideoFeatures, error) {
	return s.features, nil
}

// DeleteUserData is the store method these tests never call. Present because
// the interface has it, and answering zero is the honest stub: nothing is
// stored here to be deleted.
func (stubStore) DeleteUserData(context.Context, string, bool) (int64, int64, error) {
	return 0, 0, nil
}

type stubStore struct {
	profile   domain.UserProfile
	retention map[string]float32
	coverage  map[string]int
	// Set to have ImpressionCoverage fail, so that the feed's behaviour when it
	// cannot tell what has been shown can be asserted rather than assumed.
	coverageErr error
}

func (s stubStore) ImpressionCoverage(context.Context) (map[string]int, error) {
	return s.coverage, s.coverageErr
}

func (stubStore) AppendSignal(context.Context, domain.Signal) error         { return nil }
func (stubStore) RecordImpressions(context.Context, string, []string) error { return nil }
func (stubStore) MostWatched(context.Context, string, int32) ([]domain.RankedVideo, error) {
	return nil, nil
}
func (s stubStore) VideoRetention(context.Context) (map[string]float32, error) {
	return s.retention, nil
}
func (s stubStore) BuildProfile(context.Context, string, time.Duration) (domain.UserProfile, error) {
	return s.profile, nil
}

func emptyProfile() domain.UserProfile {
	return domain.UserProfile{
		WatchedFraction:   map[string]float32{},
		Liked:             map[string]bool{},
		Disliked:          map[string]time.Time{},
		Subscribed:        map[string]bool{},
		RecentImpressions: map[string]bool{},
		RecentlyWatched:   map[string]bool{},
		SessionWatched:    map[string]float32{},
	}
}

func features(n int) []domain.VideoFeatures {
	out := make([]domain.VideoFeatures, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.VideoFeatures{
			VideoID:     string(rune('a'+i/26)) + string(rune('a'+i%26)),
			ChannelID:   "chan1",
			AddedAt:     time.Now(),
			PublishedAt: time.Now(),
		})
	}
	return out
}

// The defect this guards: recording impressions lowers the score of everything
// just served, so a re-ranked page 2 pulls up videos that were on page 1.
func TestPagingNeverRepeatsAVideo(t *testing.T) {
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features(50)})
	ranker.snapshots = NewSnapshotStore(time.Minute)

	ctx := context.Background()
	first, err := ranker.GetFeedPage(ctx, "user1", "", "", 20, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(first.Videos) != 20 {
		t.Fatalf("page 1 returned %d, want 20", len(first.Videos))
	}

	// Simulate the impression penalty landing between pages.
	profile := emptyProfile()
	for _, v := range first.Videos {
		profile.RecentImpressions[v.VideoID] = true
	}
	ranker.store = stubStore{profile: profile}

	second, err := ranker.GetFeedPage(ctx, "user1", "", first.SnapshotID, 20, 20, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}

	seen := map[string]bool{}
	for _, v := range first.Videos {
		seen[v.VideoID] = true
	}
	for _, v := range second.Videos {
		if seen[v.VideoID] {
			t.Fatalf("video %s appeared on both pages", v.VideoID)
		}
	}
}

func TestRemainingReportsHowMuchFeedIsLeft(t *testing.T) {
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features(50)})
	ranker.snapshots = NewSnapshotStore(time.Minute)

	page, err := ranker.GetFeedPage(context.Background(), "user1", "", "", 20, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if page.Remaining != 30 {
		t.Fatalf("Remaining = %d, want 30", page.Remaining)
	}
}

// The reported bug: scrolling the feed showed the same videos again on the next
// page.
//
// The cause was not pagination. A client with an infinite query refetches *every*
// page it holds, replaying each stored page parameter — page one with no token,
// page two with the token it already had. Every tokenless request built a brand
// new ordering, so page one came back from one ordering while page two was still
// reading another, and the two spliced together repeated whatever they shared.
// Measured on the real library at four duplicates in the first forty-eight.
func TestRefetchingTheFirstPageDoesNotReorderUnderTheLaterOnes(t *testing.T) {
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features(200)})
	ctx := context.Background()

	first, err := ranker.GetFeedPage(ctx, "user1", "", "", 24, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	second, err := ranker.GetFeedPage(ctx, "user1", "", first.SnapshotID, 24, 24,
		DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}

	// The refetch: page one again with nothing, page two again with its token.
	refetched, err := ranker.GetFeedPage(ctx, "user1", "", "", 24, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("refetched first page: %v", err)
	}

	seen := map[string]bool{}
	for _, v := range refetched.Videos {
		seen[v.VideoID] = true
	}
	for _, v := range second.Videos {
		if seen[v.VideoID] {
			t.Fatalf("%s is on the refetched first page and on the second; the "+
				"orderings have come apart", v.VideoID)
		}
	}
	if refetched.SnapshotID != first.SnapshotID {
		t.Fatalf("the refetch started a new session (%s, was %s) rather than "+
			"reading the one already open", refetched.SnapshotID, first.SnapshotID)
	}
}

// Rewinding is what turned a lost session into a repeat. A client asking for
// page three has already read pages one and two, whatever the server has since
// forgotten; handing it page one is the duplication, not a recovery from it.
func TestAnExpiredSessionDoesNotSendTheViewerBackToTheTop(t *testing.T) {
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features(200)})

	// A token from a session the server no longer holds — aged out, or lost to a
	// restart. The viewer has read two pages regardless of what the server kept.
	page, err := ranker.GetFeedPage(context.Background(), "user1", "", "user1|#999", 24, 48,
		DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("GetFeedPage: %v", err)
	}
	if len(page.Videos) == 0 {
		t.Fatal("an expired session returned nothing at all")
	}
	if page.Videos[0].VideoID == "aa" {
		t.Fatal("a request for the third page came back with the first")
	}
}

// A watched video still starts a new session, which is the whole point of
// invalidating: the next time Home is opened it reflects what was just watched.
func TestWatchingSomethingStillEarnsAFreshOrdering(t *testing.T) {
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features(200)})
	ctx := context.Background()

	first, err := ranker.GetFeedPage(ctx, "user1", "", "", 24, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}

	if err := ranker.RecordSignal(ctx, domain.Signal{
		UserID: "user1", Type: domain.SignalWatch, VideoID: "aa", WatchedFraction: 0.5,
	}); err != nil {
		t.Fatalf("RecordSignal: %v", err)
	}

	next, err := ranker.GetFeedPage(ctx, "user1", "", "", 24, 0, DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("page after watching: %v", err)
	}
	if next.SnapshotID == first.SnapshotID {
		t.Fatal("watching something left the viewer on the same frozen ordering")
	}
}
