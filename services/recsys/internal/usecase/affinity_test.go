package usecase

import (
	"math"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

func TestLikeAffinityScoresTopicAboveChannelAboveHashtag(t *testing.T) {
	features := []domain.VideoFeatures{
		{VideoID: "liked", ChannelID: "chanA", Topics: []string{"Music"}, Hashtags: []string{"live"}},
		{VideoID: "sameTopic", ChannelID: "chanB", Topics: []string{"Music"}},
		{VideoID: "sameChannel", ChannelID: "chanA", Topics: []string{"Tech"}},
		{VideoID: "sameHashtag", ChannelID: "chanC", Topics: []string{"Tech"}, Hashtags: []string{"live"}},
		{VideoID: "unrelated", ChannelID: "chanD", Topics: []string{"Gaming"}},
	}

	affinity := buildLikeAffinity(features, map[string]bool{"liked": true})

	topic := affinity.Score(features[1])
	channel := affinity.Score(features[2])
	hashtag := affinity.Score(features[3])
	none := affinity.Score(features[4])

	if !(topic > channel && channel > hashtag && hashtag > none) {
		t.Fatalf("ordering wrong: topic=%v channel=%v hashtag=%v none=%v",
			topic, channel, hashtag, none)
	}
	if none != 0 {
		t.Errorf("an unrelated video scored %v, want 0", none)
	}
	if math.Abs(topic-1.0) > 1e-9 {
		t.Errorf("one like on one topic should score 1.0, got %v", topic)
	}
	if math.Abs(channel-0.8) > 1e-9 {
		t.Errorf("channel weight = %v, want 0.8", channel)
	}
	if math.Abs(hashtag-0.5) > 1e-9 {
		t.Errorf("hashtag weight = %v, want 0.5", hashtag)
	}
}

func TestLikeAffinityAccumulatesAcrossLikes(t *testing.T) {
	features := []domain.VideoFeatures{
		{VideoID: "l1", ChannelID: "chanA", Topics: []string{"Music"}},
		{VideoID: "l2", ChannelID: "chanB", Topics: []string{"Music"}},
		{VideoID: "candidate", ChannelID: "chanZ", Topics: []string{"Music"}},
	}

	one := buildLikeAffinity(features, map[string]bool{"l1": true})
	two := buildLikeAffinity(features, map[string]bool{"l1": true, "l2": true})

	if two.Score(features[2]) <= one.Score(features[2]) {
		t.Fatalf("two likes on a topic must outweigh one: %v vs %v",
			two.Score(features[2]), one.Score(features[2]))
	}
}

func TestVideoWithMultipleMatchesScoresHigherThanEither(t *testing.T) {
	features := []domain.VideoFeatures{
		{VideoID: "liked", ChannelID: "chanA", Topics: []string{"Music"}},
		{VideoID: "both", ChannelID: "chanA", Topics: []string{"Music"}},
		{VideoID: "topicOnly", ChannelID: "chanB", Topics: []string{"Music"}},
	}

	affinity := buildLikeAffinity(features, map[string]bool{"liked": true})
	if affinity.Score(features[1]) <= affinity.Score(features[2]) {
		t.Fatal("matching on both topic and channel must beat matching on topic alone")
	}
}

func TestNoLikesMeansNoInfluence(t *testing.T) {
	features := []domain.VideoFeatures{{VideoID: "a", ChannelID: "chanA", Topics: []string{"Music"}}}
	affinity := buildLikeAffinity(features, map[string]bool{})
	if affinity.Score(features[0]) != 0 {
		t.Fatal("a user who has liked nothing must get no affinity boost")
	}
}

func TestDislikeAffinityReadsTheSameAxesAsALike(t *testing.T) {
	// The asymmetry this exists for: rejecting a video used to remove that
	// video and teach the feed nothing, so turning down ten of a kind left the
	// eleventh exactly where it was.
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "no", ChannelID: "chanA", Topics: []string{"Music"}, Hashtags: []string{"live"}},
		{VideoID: "sameTopic", ChannelID: "chanB", Topics: []string{"Music"}},
		{VideoID: "sameChannel", ChannelID: "chanA", Topics: []string{"Tech"}},
		{VideoID: "unrelated", ChannelID: "chanD", Topics: []string{"Gaming"}},
	}

	affinity := buildDislikeAffinity(features, map[string]time.Time{"no": now}, now)

	if got := affinity.Score(features[1]); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("shared topic scored %v, want the same 1.0 a like earns", got)
	}
	if got := affinity.Score(features[2]); math.Abs(got-0.8) > 1e-9 {
		t.Errorf("shared channel scored %v, want 0.8", got)
	}
	if got := affinity.Score(features[3]); got != 0 {
		t.Errorf("an unrelated video scored %v, want 0", got)
	}
}

func TestADislikeFadesWithAge(t *testing.T) {
	// Every other signal in the ranker ages. This one could not, because it was
	// only ever a boolean.
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "no", ChannelID: "chanA", Topics: []string{"Music"}},
		{VideoID: "candidate", ChannelID: "chanB", Topics: []string{"Music"}},
	}

	fresh := buildDislikeAffinity(features,
		map[string]time.Time{"no": now.Add(-24 * time.Hour)}, now)
	old := buildDislikeAffinity(features,
		map[string]time.Time{"no": now.Add(-180 * 24 * time.Hour)}, now)

	if !(old.Score(features[1]) < fresh.Score(features[1])) {
		t.Fatalf("a six-month-old dislike (%v) must weigh less than yesterday's (%v)",
			old.Score(features[1]), fresh.Score(features[1]))
	}
	// Two half-lives at ninety days, so a quarter of its original say.
	if got := old.Score(features[1]); math.Abs(got-0.25) > 0.01 {
		t.Errorf("after 180 days the topic scored %v, want about 0.25", got)
	}
}

func TestADislikeWithNoRecordedTimeStillCounts(t *testing.T) {
	// Signals written before the timestamp was carried through have none.
	// Reading that as "infinitely old" would discard them silently.
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "no", ChannelID: "chanA", Topics: []string{"Music"}},
		{VideoID: "candidate", ChannelID: "chanB", Topics: []string{"Music"}},
	}

	affinity := buildDislikeAffinity(features, map[string]time.Time{"no": {}}, now)
	if got := affinity.Score(features[1]); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("an undated dislike scored %v, want its full 1.0", got)
	}
}
