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
	if fresh.Reason != domain.ReasonDiscovery {
		t.Fatalf("reason = %q, want %q", fresh.Reason, domain.ReasonDiscovery)
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

func TestUpNextDoesNotOfferBackSomethingJustWatched(t *testing.T) {
	// The reported loop, reduced. Two videos on one channel sharing a topic are
	// each other's strongest match — same-channel and shared-tags together beat
	// everything else in this function — so pressing next twice returned the
	// viewer to where they started, forever.
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "a", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now},
		{VideoID: "b", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now},
		{VideoID: "c", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now},
		{VideoID: "d", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now},
	}
	profile := emptyProfile()
	// Arrived at "b" from "a", moments ago.
	profile.RecentlyWatched["a"] = true
	profile.RecentlyWatched["b"] = true

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.GetUpNext(context.Background(), "viewer", "b", "", 20)
	if err != nil {
		t.Fatalf("GetUpNext: %v", err)
	}
	if len(ranked) == 0 {
		t.Fatal("expected suggestions")
	}
	if ranked[0].VideoID == "a" {
		t.Fatal("up-next offered back the video just watched; the loop is still there")
	}
}

func TestUpNextStillOffersSomethingWatchedLongAgo(t *testing.T) {
	// The penalty is about "just now", not about ever. A library this size
	// cannot afford to retire everything on first viewing.
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "old_favourite", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now},
		{VideoID: "current", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now},
	}
	profile := emptyProfile()
	// Watched at some point, but not in this sitting.
	profile.WatchedFraction["old_favourite"] = 0.99

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.GetUpNext(context.Background(), "viewer", "current", "", 20)
	if err != nil {
		t.Fatalf("GetUpNext: %v", err)
	}
	if len(ranked) != 1 || ranked[0].VideoID != "old_favourite" {
		t.Fatalf("a video watched long ago was dropped from up-next: %+v", ranked)
	}
}

func TestUpNextPrefersRelatednessOverGeneralTaste(t *testing.T) {
	// Observed: an Entertainment video from a gaming channel offered twenty
	// consecutive music videos, none sharing its channel or its topic, purely
	// because music is what this viewer watches most. CLAUDE.md §6 fixes the
	// order — same channel, then same tag, then general affinity — and at full
	// weight affinity did not come third, it won.
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "current", ChannelID: "ch_games", Topics: []string{"Entertainment"}, AddedAt: now},
		// Shares the current video's topic, from a channel never watched.
		{VideoID: "related", ChannelID: "ch_other_games", Topics: []string{"Entertainment"}, AddedAt: now},
		// The viewer's favourite channel and topic, unrelated to what is playing.
		{VideoID: "favourite", ChannelID: "ch_music", Topics: []string{"Music"}, AddedAt: now},
		// What builds that taste.
		{VideoID: "history_1", ChannelID: "ch_music", Topics: []string{"Music"}, AddedAt: now},
		{VideoID: "history_2", ChannelID: "ch_music", Topics: []string{"Music"}, AddedAt: now},
	}
	profile := profileWith(map[string]float32{"history_1": 1.0, "history_2": 1.0})

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.GetUpNext(context.Background(), "viewer", "current", "", 20)
	if err != nil {
		t.Fatalf("GetUpNext: %v", err)
	}
	if len(ranked) == 0 {
		t.Fatal("expected suggestions")
	}
	if ranked[0].VideoID != "related" {
		t.Fatalf("up-next led with %q; a video sharing the current topic must beat "+
			"one that merely matches general taste", ranked[0].VideoID)
	}
}

func TestTopicAffinityTakesTheBestMatchNotTheSum(t *testing.T) {
	// Summing rewards a video for carrying more labels rather than for being a
	// better match: "Music" plus "Vietnamese music" collected the affinity twice
	// and outscored an equally well-liked video with one topic.
	features := []domain.VideoFeatures{
		{VideoID: "seen", ChannelID: "ch", Topics: []string{"Music", "Vietnamese music"}},
	}
	affinity := buildWatchAffinity(features, map[string]float32{"seen": 1.0})

	twoLabels := affinity.TopicScore(domain.VideoFeatures{
		Topics: []string{"Music", "Vietnamese music"},
	})
	oneLabel := affinity.TopicScore(domain.VideoFeatures{Topics: []string{"Music"}})

	if twoLabels > oneLabel {
		t.Fatalf("two labels scored %.2f against one at %.2f; labels are being counted, "+
			"not matched", twoLabels, oneLabel)
	}
	if twoLabels > 1.0 {
		t.Fatalf("topic affinity %.2f exceeds the 0..1 each axis is normalised to", twoLabels)
	}
}
