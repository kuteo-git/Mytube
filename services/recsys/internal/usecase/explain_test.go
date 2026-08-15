package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// The property that makes the endpoint worth having: it reports the ranker's
// own arithmetic, not a second implementation of it. An explanation that can
// disagree with the feed is worse than none, because it would be trusted.
func TestTheExplanationScoresAgreeWithTheFeed(t *testing.T) {
	now := time.Now()
	profile := emptyProfile()
	profile.Subscribed["ch_1"] = true
	profile.WatchedFraction["v3"] = 0.4
	features := []domain.VideoFeatures{
		{VideoID: "v1", ChannelID: "ch_1", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now.Add(-time.Hour)},
		{VideoID: "v2", ChannelID: "ch_2", Topics: []string{"Gaming"}, AddedAt: now, PublishedAt: now.Add(-96 * time.Hour)},
		{VideoID: "v3", ChannelID: "ch_3", Topics: []string{"Music"}, AddedAt: now, PublishedAt: now.Add(-96 * time.Hour)},
	}
	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})

	explained, err := ranker.ExplainFeed(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("ExplainFeed: %v", err)
	}
	ranked, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	byID := map[string]domain.RankedVideo{}
	for _, v := range ranked {
		byID[v.VideoID] = v
	}
	for _, e := range explained {
		if e.Excluded != "" {
			continue
		}
		got, ok := byID[e.VideoID]
		if !ok {
			t.Fatalf("%s was explained as included but is not in the feed", e.VideoID)
		}
		// Not exactly equal: the two calls read the clock separately, and every
		// recency term moves with it.
		if diff := e.Score - got.Score; diff > 1e-6 || diff < -1e-6 {
			t.Fatalf("%s explained as %.9f, ranked as %.9f", e.VideoID, e.Score, got.Score)
		}
		if e.Reason != got.Reason {
			t.Fatalf("%s explained as %q, ranked as %q", e.VideoID, e.Reason, got.Reason)
		}
	}
}

// Where a video landed is most of what somebody is asking, and it cannot be read
// off the score: the quota interleaves by slot and the channel cap defers.
func TestTheExplanationReportsFeedPosition(t *testing.T) {
	now := time.Now()
	var features []domain.VideoFeatures
	for i := 0; i < 30; i++ {
		features = append(features, domain.VideoFeatures{
			VideoID:     string(rune('a' + i)),
			ChannelID:   "ch_1",
			AddedAt:     now,
			PublishedAt: now.Add(-96 * time.Hour),
		})
	}
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features})

	explained, err := ranker.ExplainFeed(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("ExplainFeed: %v", err)
	}
	if len(explained) == 0 {
		t.Fatal("nothing was explained")
	}
	if explained[0].Position != 0 {
		t.Fatalf("the first explanation reports position %d", explained[0].Position)
	}
}

// The question the endpoint most often has to answer is about a video that is
// not there, and "not there" is many different situations.
func TestAnExcludedVideoSaysWhichRuleDroppedIt(t *testing.T) {
	now := time.Now()
	profile := emptyProfile()
	profile.Disliked["rejected"] = now
	profile.WatchedFraction["finished"] = 0.99
	features := []domain.VideoFeatures{
		{VideoID: "rejected", ChannelID: "ch_1", AddedAt: now, PublishedAt: now.Add(-time.Hour)},
		{VideoID: "finished", ChannelID: "ch_2", AddedAt: now, PublishedAt: now.Add(-time.Hour)},
		{VideoID: "ancient", ChannelID: "ch_3", AddedAt: now, PublishedAt: now.Add(-400 * 24 * time.Hour)},
		{VideoID: "undated", ChannelID: "ch_4", AddedAt: now},
	}
	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})

	explained, err := ranker.ExplainFeed(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("ExplainFeed: %v", err)
	}

	want := map[string]string{
		"rejected": excludedDisliked,
		"finished": excludedWatchedEnough,
		"ancient":  excludedTooOld,
		"undated":  excludedNoPublishDate,
	}
	for _, e := range explained {
		if got := want[e.VideoID]; got != e.Excluded {
			t.Fatalf("%s was excluded as %q, want %q", e.VideoID, e.Excluded, got)
		}
		if e.Position != -1 {
			t.Fatalf("%s is excluded but reports position %d", e.VideoID, e.Position)
		}
	}
}

// A video upstream will not hand over is not a video to put on the home page.
//
// It stays reachable through search and the channel page — the library does
// hold it, and knowing why it cannot be played is worth something — but the
// feed offers things to press, and this one cannot open.
func TestUnavailableVideosAreKeptOutOfTheFeed(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{
			VideoID:     "members_only",
			ChannelID:   "ch_1",
			AddedAt:     now,
			PublishedAt: now.Add(-time.Hour),
			MediaState:  "MEDIA_STATE_UNAVAILABLE",
		},
		{
			VideoID:     "watchable",
			ChannelID:   "ch_2",
			AddedAt:     now,
			PublishedAt: now.Add(-time.Hour),
			MediaState:  "MEDIA_STATE_READY",
		},
	}
	ranker := NewRanker(stubStore{profile: emptyProfile()}, stubFeatures{features: features})

	explained, err := ranker.ExplainFeed(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("ExplainFeed: %v", err)
	}

	for _, e := range explained {
		switch e.VideoID {
		case "members_only":
			if e.Excluded != excludedMediaUnavailable {
				t.Fatalf("excluded as %q, want %q", e.Excluded, excludedMediaUnavailable)
			}
			if e.Position != -1 {
				t.Fatalf("excluded but reports position %d", e.Position)
			}
		case "watchable":
			if e.Excluded != "" {
				t.Fatalf("a ready video was excluded as %q", e.Excluded)
			}
		}
	}
}
