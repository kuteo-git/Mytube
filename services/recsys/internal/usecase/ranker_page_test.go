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

type stubStore struct{ profile domain.UserProfile }

func (stubStore) AppendSignal(context.Context, domain.Signal) error         { return nil }
func (stubStore) RecordImpressions(context.Context, string, []string) error { return nil }
func (s stubStore) BuildProfile(context.Context, string, time.Duration) (domain.UserProfile, error) {
	return s.profile, nil
}

func emptyProfile() domain.UserProfile {
	return domain.UserProfile{
		WatchedFraction:   map[string]float32{},
		Liked:             map[string]bool{},
		Disliked:          map[string]bool{},
		Subscribed:        map[string]bool{},
		RecentImpressions: map[string]bool{},
	}
}

func features(n int) []domain.VideoFeatures {
	out := make([]domain.VideoFeatures, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.VideoFeatures{
			VideoID:   string(rune('a'+i/26)) + string(rune('a'+i%26)),
			ChannelID: "chan1",
			AddedAt:   time.Now(),
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
	first, err := ranker.GetFeedPage(ctx, "user1", "", "", 20, 0)
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

	second, err := ranker.GetFeedPage(ctx, "user1", "", first.SnapshotID, 20, 20)
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

	page, err := ranker.GetFeedPage(context.Background(), "user1", "", "", 20, 0)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	if page.Remaining != 30 {
		t.Fatalf("Remaining = %d, want 30", page.Remaining)
	}
}
