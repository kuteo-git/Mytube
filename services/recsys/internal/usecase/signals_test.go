package usecase

import (
	"context"
	"errors"
	"fmt"
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
		{VideoID: "bounced", ChannelID: "ch_a", Topics: []string{"Cooking"}, AddedAt: addedAt, PublishedAt: addedAt},
		{VideoID: "fresh", ChannelID: "ch_b", Topics: []string{"Gaming"}, AddedAt: addedAt, PublishedAt: addedAt},
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

	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
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

	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
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
		{VideoID: "holds", ChannelID: "ch_a", AddedAt: now, PublishedAt: now},
		{VideoID: "loses", ChannelID: "ch_b", AddedAt: now, PublishedAt: now},
	}
	store := stubStore{
		profile:   emptyProfile(),
		retention: map[string]float32{"holds": 0.9, "loses": 0.05},
	}
	ranker := NewRanker(store, stubFeatures{features: features})

	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
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
		{VideoID: "seen_1", ChannelID: "ch_a", Topics: []string{"Cooking"}, AddedAt: now, PublishedAt: now},
		{VideoID: "seen_2", ChannelID: "ch_b", Topics: []string{"Cooking"}, AddedAt: now, PublishedAt: now},
		{VideoID: "same_topic", ChannelID: "ch_c", Topics: []string{"Cooking"}, AddedAt: now, PublishedAt: now},
		{VideoID: "other_topic", ChannelID: "ch_d", Topics: []string{"Gaming"}, AddedAt: now, PublishedAt: now},
	}
	store := stubStore{profile: profileWith(map[string]float32{
		"seen_1": 0.9,
		"seen_2": 0.85,
	})}
	ranker := NewRanker(store, stubFeatures{features: features})

	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
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
			VideoID: id, ChannelID: "ch_a", Topics: []string{"Cooking"}, AddedAt: now, PublishedAt: now,
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
		{VideoID: "finished", ChannelID: "ch_loved", Topics: []string{"Cooking"}, AddedAt: now, PublishedAt: now},
		{VideoID: "abandoned", ChannelID: "ch_meh", Topics: []string{"Gaming"}, AddedAt: now, PublishedAt: now},
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

	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
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
		{VideoID: "a", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now},
		{VideoID: "b", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now},
		{VideoID: "c", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now},
		{VideoID: "d", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now},
	}
	profile := emptyProfile()
	// Arrived at "b" from "a", moments ago.
	profile.RecentlyWatched["a"] = true
	profile.RecentlyWatched["b"] = true

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, _, err := ranker.GetUpNext(context.Background(), "viewer", "b", "", 20, 0)
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
		{VideoID: "old_favourite", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now},
		{VideoID: "current", ChannelID: "ch", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now},
	}
	profile := emptyProfile()
	// Watched at some point, but not in this sitting.
	profile.WatchedFraction["old_favourite"] = 0.99

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, _, err := ranker.GetUpNext(context.Background(), "viewer", "current", "", 20, 0)
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
		{VideoID: "current", ChannelID: "ch_games", Topics: []string{"Entertainment"}, AddedAt: now, PublishedAt: now},
		// Shares the current video's topic, from a channel never watched.
		{VideoID: "related", ChannelID: "ch_other_games", Topics: []string{"Entertainment"}, AddedAt: now, PublishedAt: now},
		// The viewer's favourite channel and topic, unrelated to what is playing.
		{VideoID: "favourite", ChannelID: "ch_music", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now},
		// What builds that taste.
		{VideoID: "history_1", ChannelID: "ch_music", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now},
		{VideoID: "history_2", ChannelID: "ch_music", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now},
	}
	profile := profileWith(map[string]float32{"history_1": 1.0, "history_2": 1.0})

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, _, err := ranker.GetUpNext(context.Background(), "viewer", "current", "", 20, 0)
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

// present reports whether a video survived into the ranking at all.
func present(ranked []domain.RankedVideo, videoID string) bool {
	for _, v := range ranked {
		if v.VideoID == videoID {
			return true
		}
	}
	return false
}

func TestDislikingMostOfAChannelSuppressesIt(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{}
	for i, id := range []string{"a1", "a2", "a3", "a4", "a5"} {
		features = append(features, domain.VideoFeatures{
			VideoID: id, ChannelID: "ch_small", Topics: []string{"Gaming"}, AddedAt: now, PublishedAt: now,
		})
		_ = i
	}
	profile := emptyProfile()
	profile.Disliked["a1"] = now
	profile.Disliked["a2"] = now
	profile.Disliked["a3"] = now

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if present(ranked, "a4") || present(ranked, "a5") {
		t.Fatal("three of five turned down is a verdict on the channel; the rest must go too")
	}
}

func TestThreeDislikesInALargeChannelAreJustThreeDislikes(t *testing.T) {
	// The rule used to be a bare count, so this case and the one above were
	// indistinguishable: three rejections out of two hundred silenced a channel
	// the viewer had barely disagreed with.
	now := time.Now()
	var features []domain.VideoFeatures
	for i := 0; i < 200; i++ {
		features = append(features, domain.VideoFeatures{
			VideoID:   fmt.Sprintf("b%d", i),
			ChannelID: "ch_big",
			Topics:    []string{"Gaming"},
			AddedAt:   now,
			PublishedAt: now,
		})
	}
	profile := emptyProfile()
	profile.Disliked["b0"] = now
	profile.Disliked["b1"] = now
	profile.Disliked["b2"] = now

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if !present(ranked, "b100") {
		t.Fatal("three rejections out of two hundred must not remove the channel")
	}
	for _, id := range []string{"b0", "b1", "b2"} {
		if present(ranked, id) {
			t.Fatalf("%s was turned down and must still be gone", id)
		}
	}
}

func TestRejectingATopicPushesTheRestOfItDown(t *testing.T) {
	// What "not interested" is taken to mean beyond the video it was pressed
	// on. The two candidates differ only in topic, so nothing else can explain
	// the gap.
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "rejected", ChannelID: "ch_a", Topics: []string{"Reaction"}, AddedAt: now, PublishedAt: now},
		{VideoID: "sameTopic", ChannelID: "ch_b", Topics: []string{"Reaction"}, AddedAt: now, PublishedAt: now},
		{VideoID: "otherTopic", ChannelID: "ch_c", Topics: []string{"Cooking"}, AddedAt: now, PublishedAt: now},
	}
	profile := emptyProfile()
	profile.Disliked["rejected"] = now

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	same := scoreOf(t, ranked, "sameTopic")
	other := scoreOf(t, ranked, "otherTopic")
	if same.Score >= other.Score {
		t.Fatalf("a video sharing the rejected topic scored %.2f against %.2f for an "+
			"unrelated one; the rejection taught the feed nothing", same.Score, other.Score)
	}
	// Tilted, not deleted: the point of the weight being a third of a like's.
	if !present(ranked, "sameTopic") {
		t.Fatal("one rejection must not empty a subject")
	}
}

func TestEnoughRejectionsSuppressALargeChannelWhateverTheShare(t *testing.T) {
	// The case the share alone could never reach, taken from the library:
	// NoCopyrightSounds at 162 videos, twenty of them turned down. That is 12%,
	// so the channel needed forty-nine rejections before it counted — and every
	// scan added more of its videos, growing the denominator, so pressing "not
	// interested" again moved the share *down*. The one thing a viewer will try
	// when a control seems not to work is to use it more, and here that made it
	// work less.
	now := time.Now()
	var features []domain.VideoFeatures
	for i := 0; i < 162; i++ {
		features = append(features, domain.VideoFeatures{
			VideoID:   fmt.Sprintf("c%d", i),
			ChannelID: "ch_large",
			Topics:    []string{"Music"},
			AddedAt:   now,
			PublishedAt: now,
		})
	}
	profile := emptyProfile()
	for i := 0; i < 20; i++ {
		profile.Disliked[fmt.Sprintf("c%d", i)] = now
	}

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if present(ranked, "c100") {
		t.Fatal("twenty rejections is a verdict on the channel, whatever share of it they are")
	}
}

func TestSubscribingStillOverridesEnoughRejections(t *testing.T) {
	// Following a channel is a deliberate statement and outranks the passive
	// one. The ceiling must not become a way to lose a channel you asked for.
	now := time.Now()
	var features []domain.VideoFeatures
	for i := 0; i < 162; i++ {
		features = append(features, domain.VideoFeatures{
			VideoID:   fmt.Sprintf("d%d", i),
			ChannelID: "ch_followed",
			Topics:    []string{"Music"},
			AddedAt:   now,
			PublishedAt: now,
		})
	}
	profile := emptyProfile()
	profile.Subscribed["ch_followed"] = true
	for i := 0; i < 20; i++ {
		profile.Disliked[fmt.Sprintf("d%d", i)] = now
	}

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if !present(ranked, "d100") {
		t.Fatal("a followed channel must survive its own rejections")
	}
}

func TestFreshnessBoostMultiplier(t *testing.T) {
	now := time.Now()
	highViews := int64(50000)
	bonus := freshnessBoost(now.Add(-1*time.Hour), highViews, now, freshnessWindow)
	none := freshnessBoost(now.Add(-49*time.Hour), highViews, now, freshnessWindow)
	if bonus <= 1.0 {
		t.Fatalf("fresh video with %d views got %.2f, want > 1.0", highViews, bonus)
	}
	if none != 1.0 {
		t.Fatalf("video outside the window got %.2f, want 1.0", none)
	}
}

func TestFreshnessBoostNoViewsStillGivesFloorBoost(t *testing.T) {
	now := time.Now()
	score := freshnessBoost(now.Add(-30*time.Minute), 0, now, freshnessWindow)
	if score <= 1.0 {
		t.Fatalf("fresh video with no views got %.2f, want > 1.0 (floor boost)", score)
	}
}

func TestFreshnessBoostNoPublishedDateGivesNoBonus(t *testing.T) {
	now := time.Now()
	score := freshnessBoost(time.Time{}, 100000, now, freshnessWindow)
	if score != 1.0 {
		t.Fatalf("video with no published date got %.2f, want 1.0", score)
	}
}

func TestFreshnessBoostMoreViewsOutranksFewerViews(t *testing.T) {
	now := time.Now()
	publishedAt := now.Add(-2 * time.Hour)
	popular := freshnessBoost(publishedAt, 500000, now, freshnessWindow)
	unknown := freshnessBoost(publishedAt, 100, now, freshnessWindow)
	if popular <= unknown {
		t.Fatalf("500k views scored %.2f against 100 views at %.2f",
			popular, unknown)
	}
}

func TestLanguageFilterHidesUnwantedLanguage(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "en_video", ChannelID: "ch_a", Topics: []string{"News"}, AddedAt: now, PublishedAt: now, Language: "en"},
		{VideoID: "ar_video", ChannelID: "ch_b", Topics: []string{"News"}, AddedAt: now, PublishedAt: now, Language: "ar"},
		{VideoID: "vi_video", ChannelID: "ch_c", Topics: []string{"News"}, AddedAt: now, PublishedAt: now, Language: "vi"},
	}
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, []string{"en", "vi"}, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if !present(ranked, "en_video") {
		t.Fatal("English video was filtered despite being allowed")
	}
	if !present(ranked, "vi_video") {
		t.Fatal("Vietnamese video was filtered despite being allowed")
	}
	if present(ranked, "ar_video") {
		t.Fatal("Arabic video appeared despite not being allowed")
	}
}

func TestLanguageFilterHidesUnknownLanguage(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "known", ChannelID: "ch_a", Topics: []string{"News"}, AddedAt: now, PublishedAt: now, Language: "en"},
		{VideoID: "unknown", ChannelID: "ch_b", Topics: []string{"News"}, AddedAt: now, PublishedAt: now, Language: ""},
	}
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, []string{"en", "vi"}, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if !present(ranked, "known") {
		t.Fatal("known-language video was filtered")
	}
	if present(ranked, "unknown") {
		t.Fatal("video with no language appeared despite a filter being set")
	}
}

func TestNoLanguageFilterShowsEverything(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "en_video", ChannelID: "ch_a", Topics: []string{"News"}, AddedAt: now, PublishedAt: now, Language: "en"},
		{VideoID: "ar_video", ChannelID: "ch_b", Topics: []string{"News"}, AddedAt: now, PublishedAt: now, Language: "ar"},
		{VideoID: "no_lang", ChannelID: "ch_c", Topics: []string{"News"}, AddedAt: now, PublishedAt: now, Language: ""},
	}
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if len(ranked) != 3 {
		t.Fatalf("no language filter should show all 3 videos, got %d", len(ranked))
	}
}

// discoveryFixture is a viewer with no history at all, so every video falls into
// the discovery slot and the only thing separating them is how often each has
// already been offered.
func discoveryFixture(now time.Time, ids ...string) []domain.VideoFeatures {
	out := make([]domain.VideoFeatures, 0, len(ids))
	for i, id := range ids {
		out = append(out, domain.VideoFeatures{
			VideoID:     id,
			ChannelID:   fmt.Sprintf("ch_%d", i),
			Topics:      []string{"Gaming"},
			AddedAt:     now,
			PublishedAt: now.Add(-72 * time.Hour),
		})
	}
	return out
}

// What "something new" is supposed to mean. Before this, the discovery share
// ranked by score over a set that barely moves, so the same handful of
// unfamiliar videos came back every time while most of the library was never
// offered once.
func TestSomethingNewPrefersWhatNobodyHasBeenShown(t *testing.T) {
	now := time.Now()
	features := discoveryFixture(now, "neverShown", "shownOften")
	store := stubStore{
		profile:  emptyProfile(),
		coverage: map[string]int{"shownOften": 12},
	}

	ranker := NewRanker(store, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	unseen := scoreOf(t, ranked, "neverShown").Score
	stale := scoreOf(t, ranked, "shownOften").Score
	if unseen <= stale {
		t.Fatalf("a video nobody has seen scored %.2f against %.2f for one offered "+
			"a dozen times and never opened", unseen, stale)
	}
}

// The bonus decays rather than switching off, so a video offered twice still
// beats one offered twenty times. A step function would sort the whole library
// into "new" and "not new" and then have nothing left to say.
func TestTheExplorationBonusDecaysWithEveryShowing(t *testing.T) {
	now := time.Now()
	features := discoveryFixture(now, "once", "several", "many")
	store := stubStore{
		profile:  emptyProfile(),
		coverage: map[string]int{"once": 1, "several": 4, "many": 20},
	}

	ranker := NewRanker(store, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	once := scoreOf(t, ranked, "once").Score
	several := scoreOf(t, ranked, "several").Score
	many := scoreOf(t, ranked, "many").Score
	if !(once > several && several > many) {
		t.Fatalf("scores by exposure came out %.3f / %.3f / %.3f, want strictly "+
			"decreasing", once, several, many)
	}
}

// Same posture as VideoRetention: a query that fails must cost the feed a signal,
// never its contents.
func TestLosingTheImpressionCountsDoesNotEmptyTheFeed(t *testing.T) {
	now := time.Now()
	features := discoveryFixture(now, "a", "b")
	store := stubStore{profile: emptyProfile(), coverageErr: errors.New("no table")}

	ranker := NewRanker(store, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll returned an error rather than ranking on: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked videos, got %d", len(ranked))
	}
}

// sessionFixture is a viewer with a long history of one subject and a candidate
// from each of two subjects, so that changing only the session shows what the
// session does.
func sessionFixture(now time.Time) ([]domain.VideoFeatures, domain.UserProfile) {
	var features []domain.VideoFeatures
	profile := emptyProfile()
	// Two years of music.
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("history%d", i)
		features = append(features, domain.VideoFeatures{
			VideoID: id, ChannelID: "ch_music", Topics: []string{"Music"},
			AddedAt: now, PublishedAt: now.Add(-24 * time.Hour),
		})
		profile.WatchedFraction[id] = 1.0
	}
	// One cooking video, watched this afternoon.
	features = append(features, domain.VideoFeatures{
		VideoID: "cooked", ChannelID: "ch_kitchen", Topics: []string{"Cooking"},
		AddedAt: now, PublishedAt: now.Add(-24 * time.Hour),
	})
	profile.WatchedFraction["cooked"] = 0.9

	// The two candidates, identical but for their subject.
	features = append(features,
		domain.VideoFeatures{
			VideoID: "moreMusic", ChannelID: "ch_music2", Topics: []string{"Music"},
			AddedAt: now, PublishedAt: now.Add(-24 * time.Hour),
		},
		domain.VideoFeatures{
			VideoID: "moreCooking", ChannelID: "ch_kitchen2", Topics: []string{"Cooking"},
			AddedAt: now, PublishedAt: now.Add(-24 * time.Hour),
		})
	return features, profile
}

func TestWhatTheViewerIsWatchingNowOutweighsWhatTheyUsuallyWatch(t *testing.T) {
	now := time.Now()
	features, profile := sessionFixture(now)

	withoutSession := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	before, err := withoutSession.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	baseline := scoreOf(t, before, "moreCooking").Score

	// The same viewer, one cooking video into a sitting.
	sessionProfile := profile
	sessionProfile.SessionWatched = map[string]float32{"cooked": 0.9}
	withSession := NewRanker(stubStore{profile: sessionProfile}, stubFeatures{features: features})
	after, err := withSession.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	if got := scoreOf(t, after, "moreCooking").Score; got <= baseline {
		t.Fatalf("another cooking video scored %.2f during a cooking session "+
			"against %.2f outside one; the session changed nothing", got, baseline)
	}
	if cooking, music := scoreOf(t, after, "moreCooking").Score,
		scoreOf(t, after, "moreMusic").Score; cooking <= music {
		t.Fatalf("an hour into cooking videos, cooking scored %.2f against music "+
			"at %.2f", cooking, music)
	}
}

// The other side of the same trade. A session is a nudge, not a takeover: two
// years of history must not be erased by one video, or the viewer loses the
// ability to change their mind back.
func TestOneVideoDoesNotEraseTheWatchHistory(t *testing.T) {
	now := time.Now()
	features, profile := sessionFixture(now)
	profile.SessionWatched = map[string]float32{"cooked": 0.9}

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	// Music is what this viewer watches, and it stays clearly on the page.
	if !present(ranked[:min(24, len(ranked))], "moreMusic") {
		t.Fatal("one cooking video pushed the viewer's whole subject off the " +
			"first screen")
	}
}

// A viewer who has just opened the app has no session, and must rank exactly as
// they did before this existed.
func TestNoSessionRanksOnTheHistoryAlone(t *testing.T) {
	now := time.Now()
	features, profile := sessionFixture(now)

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	if music, cooking := scoreOf(t, ranked, "moreMusic").Score,
		scoreOf(t, ranked, "moreCooking").Score; music <= cooking {
		t.Fatalf("with no session, the viewer's own subject scored %.2f against "+
			"%.2f for one they have watched once", music, cooking)
	}
}

// The bug report, end to end: "I don't see new videos from channels I'm
// subscribed to, the kind published 30 minutes or an hour ago."
//
// Written against rankAll rather than the quota alone because that is where it
// went wrong. Every part in isolation was correct — the score was right, the
// buckets were right — and the video still did not appear, because the quota
// shuffled the bucket before taking its share.
func TestANewUploadFromAFollowedChannelIsOnTheFirstScreen(t *testing.T) {
	now := time.Now()
	profile := emptyProfile()
	profile.Subscribed["ch_followed"] = true

	var features []domain.VideoFeatures
	// A realistic library: the followed channel has a long back catalogue, and
	// there is plenty of other material competing for the page.
	for i := 0; i < 150; i++ {
		features = append(features, domain.VideoFeatures{
			VideoID:     fmt.Sprintf("old%d", i),
			ChannelID:   "ch_followed",
			Topics:      []string{"Music"},
			AddedAt:     now.Add(-time.Duration(i) * 24 * time.Hour),
			PublishedAt: now.Add(-time.Duration(i+3) * 24 * time.Hour),
		})
	}
	for i := 0; i < 150; i++ {
		features = append(features, domain.VideoFeatures{
			VideoID:     fmt.Sprintf("other%d", i),
			ChannelID:   fmt.Sprintf("ch_%d", i),
			Topics:      []string{"Music"},
			AddedAt:     now,
			PublishedAt: now.Add(-time.Duration(i+3) * 24 * time.Hour),
		})
	}
	features = append(features, domain.VideoFeatures{
		VideoID:     "just_published",
		ChannelID:   "ch_followed",
		Topics:      []string{"Music"},
		AddedAt:     now,
		PublishedAt: now.Add(-30 * time.Minute),
	})

	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})

	// Repeated because the ordering is sampled, and "usually on the first screen"
	// is not what a subscription promises.
	for trial := 0; trial < 20; trial++ {
		ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
		if err != nil {
			t.Fatalf("rankAll: %v", err)
		}
		if !present(ranked[:24], "just_published") {
			t.Fatalf("trial %d: a video published 30 minutes ago on a followed "+
				"channel was not on the first screen", trial)
		}
	}
}

func TestFreshVideoWithHighViewsOutranksOlderVideo(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{
			VideoID: "breaking_news", ChannelID: "ch_news", Topics: []string{"News"},
			AddedAt: now, PublishedAt: now.Add(-1 * time.Hour), ViewCount: 150000,
		},
		{
			VideoID: "old_video", ChannelID: "ch_other", Topics: []string{"News"},
			AddedAt: now, PublishedAt: now.Add(-48 * time.Hour), ViewCount: 0,
		},
	}
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	fresh := scoreOf(t, ranked, "breaking_news")
	old := scoreOf(t, ranked, "old_video")
	if fresh.Score <= old.Score {
		t.Fatalf("breaking news scored %.2f, not above the old video at %.2f",
			fresh.Score, old.Score)
	}
}

func TestLanguageFilterCaseInsensitive(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "caps", ChannelID: "ch_a", Topics: []string{"News"}, AddedAt: now, PublishedAt: now, Language: "EN"},
	}
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features})
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, []string{"en"}, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if !present(ranked, "caps") {
		t.Fatal("EN (uppercase) did not match en (lowercase) filter")
	}
}
