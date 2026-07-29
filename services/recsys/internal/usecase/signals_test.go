package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// scoreOf finds a video in a ranking. Ranked slices are small here, so a scan
// is clearer than an index.
func scoreOf(t *testing.T, ranked []domain.RankedVideo, videoID string) domain.RankedVideo {
	t.Helper()
	for _, v := range ranked {
		if v.VideoID == videoID {
			return v
		}
	}
	t.Fatalf("video %q is missing from the ranking", videoID)
	return domain.RankedVideo{}
}

func profileWith(watched map[string]float32) domain.UserProfile {
	profile := emptyProfile()
	for videoID, fraction := range watched {
		profile.WatchedFraction[videoID] = fraction
	}
	return profile
}

// twoVideos returns two videos on different channels with different topics, so
// that any score difference between them is attributable to the signal under
// test rather than to shared attributes.
func twoVideos(addedAt time.Time) []domain.VideoFeatures {
	return []domain.VideoFeatures{
		{VideoID: "bounced", ChannelID: "ch_a", Topics: []string{"Cooking"}, AddedAt: addedAt},
		{VideoID: "fresh", ChannelID: "ch_b", Topics: []string{"Gaming"}, AddedAt: addedAt},
	}
}

func TestBouncedVideoScoresBelowOneNeverOpened(t *testing.T) {
	// The regression this exists for: a bare map lookup cannot tell "never
	// opened" from "opened and abandoned", so a video rejected after two
	// seconds collected the never-watched boost and kept coming back. The
	// surest way to keep something in the feed was to reject it.
	now := time.Now()
	store := stubStore{profile: profileWith(map[string]float32{"bounced": 0.01})}
	ranker := NewRanker(store, stubFeatures{features: twoVideos(now)})

	ranked, err := ranker.rankAll(context.Background(), "viewer", "")
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	bounced := scoreOf(t, ranked, "bounced")
	fresh := scoreOf(t, ranked, "fresh")

	if bounced.Score >= fresh.Score {
		t.Fatalf("a bounced video scored %.2f, not below an unopened one at %.2f",
			bounced.Score, fresh.Score)
	}
	if bounced.Reason != domain.ReasonBounced {
		t.Fatalf("reason = %q, want %q", bounced.Reason, domain.ReasonBounced)
	}
	if fresh.Reason != domain.ReasonNeverWatched {
		t.Fatalf("reason = %q, want %q", fresh.Reason, domain.ReasonNeverWatched)
	}
}

func TestUnopenedVideoIsNotTreatedAsBounced(t *testing.T) {
	// The other half of the same distinction: an empty history must leave
	// everything looking new, not looking rejected.
	now := time.Now()
	ranker := NewRanker(
		stubStore{profile: emptyProfile()},
		stubFeatures{features: twoVideos(now)},
	)

	ranked, err := ranker.rankAll(context.Background(), "viewer", "")
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	for _, v := range ranked {
		if v.Reason == domain.ReasonBounced {
			t.Fatalf("video %q was marked bounced with no watch history", v.VideoID)
		}
	}
}

func TestRetentionLiftsVideosThatHoldAnAudience(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "holds", ChannelID: "ch_a", AddedAt: now},
		{VideoID: "loses", ChannelID: "ch_b", AddedAt: now},
	}
	store := stubStore{
		profile:   emptyProfile(),
		retention: map[string]float32{"holds": 0.9, "loses": 0.05},
	}
	ranker := NewRanker(store, stubFeatures{features: features})

	ranked, err := ranker.rankAll(context.Background(), "viewer", "")
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if scoreOf(t, ranked, "holds").Score <= scoreOf(t, ranked, "loses").Score {
		t.Fatal("a video everyone finishes did not outrank one everyone abandons")
	}
}

func TestWatchingBuildsTopicAffinityWithoutAnyLikes(t *testing.T) {
	// Nine likes against 2,045 watch signals is the real ratio in this library.
	// Taste has to be readable from watching, or it is not readable at all.
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "seen_1", ChannelID: "ch_a", Topics: []string{"Cooking"}, AddedAt: now},
		{VideoID: "seen_2", ChannelID: "ch_b", Topics: []string{"Cooking"}, AddedAt: now},
		{VideoID: "same_topic", ChannelID: "ch_c", Topics: []string{"Cooking"}, AddedAt: now},
		{VideoID: "other_topic", ChannelID: "ch_d", Topics: []string{"Gaming"}, AddedAt: now},
	}
	store := stubStore{profile: profileWith(map[string]float32{
		"seen_1": 0.9,
		"seen_2": 0.85,
	})}
	ranker := NewRanker(store, stubFeatures{features: features})

	ranked, err := ranker.rankAll(context.Background(), "viewer", "")
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	same := scoreOf(t, ranked, "same_topic")
	other := scoreOf(t, ranked, "other_topic")
	if same.Score <= other.Score {
		t.Fatalf(
			"watching two Cooking videos did not lift an unseen Cooking video: %.2f vs %.2f",
			same.Score, other.Score,
		)
	}
}

func TestWatchAffinityIsBoundedByItsStrongestEntry(t *testing.T) {
	// Affinity accumulates over a history that only grows. Left unnormalised it
	// would eventually swamp every other term in the score.
	now := time.Now()
	features := make([]domain.VideoFeatures, 0, 60)
	watched := map[string]float32{}
	for i := 0; i < 60; i++ {
		id := string(rune('a'+i%26)) + string(rune('a'+i/26))
		features = append(features, domain.VideoFeatures{
			VideoID: id, ChannelID: "ch_a", Topics: []string{"Cooking"}, AddedAt: now,
		})
		watched[id] = 1.0
	}

	affinity := buildWatchAffinity(features, watched)
	for channel, value := range affinity.Channels {
		if value > 1.0 {
			t.Fatalf("channel %q affinity %.2f exceeds 1", channel, value)
		}
	}
	for topic, value := range affinity.Topics {
		if value > 1.0 {
			t.Fatalf("topic %q affinity %.2f exceeds 1", topic, value)
		}
	}
}

func TestWatchAffinityWeightsByHowMuchWasWatched(t *testing.T) {
	// Counting openings would make a video abandoned after ten seconds argue
	// for its channel exactly as hard as one watched to the end.
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "finished", ChannelID: "ch_loved", Topics: []string{"Cooking"}, AddedAt: now},
		{VideoID: "abandoned", ChannelID: "ch_meh", Topics: []string{"Gaming"}, AddedAt: now},
	}
	affinity := buildWatchAffinity(features, map[string]float32{
		"finished":  0.95,
		"abandoned": 0.05,
	})

	if affinity.Channels["ch_loved"] <= affinity.Channels["ch_meh"] {
		t.Fatalf("channel affinity ignored watch depth: %.2f vs %.2f",
			affinity.Channels["ch_loved"], affinity.Channels["ch_meh"])
	}
}

func TestRetentionFailureDoesNotEmptyTheFeed(t *testing.T) {
	// Retention is an enrichment. Losing it must cost ordering quality, not the
	// feed itself.
	now := time.Now()
	ranker := NewRanker(
		failingRetentionStore{stubStore{profile: emptyProfile()}},
		stubFeatures{features: twoVideos(now)},
	)

	ranked, err := ranker.rankAll(context.Background(), "viewer", "")
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked videos, got %d", len(ranked))
	}
}

type failingRetentionStore struct{ stubStore }

func (failingRetentionStore) VideoRetention(context.Context) (map[string]float32, error) {
	return nil, context.DeadlineExceeded
}
