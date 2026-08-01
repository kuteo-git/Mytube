package usecase

import (
	"testing"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

func repeated(reason domain.Reason, n int, prefix string) []domain.RankedVideo {
	out := make([]domain.RankedVideo, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.RankedVideo{
			VideoID: prefix + string(rune('a'+i)),
			Reason:  reason,
			Score:   float64(n - i),
		})
	}
	return out
}

// The failure this prevents: likes and subscriptions both pull the feed toward
// the familiar, and a pure score ordering under both collapses to one subject.
func TestQuotaKeepsUnwatchedVideosInTheFirstPageEvenWhenTheyScoreLowest(t *testing.T) {
	var ranked []domain.RankedVideo
	// Twenty high-scoring rewatches, then unwatched videos scoring below them.
	ranked = append(ranked, repeated(domain.ReasonRewatch, 20, "rw")...)
	ranked = append(ranked, repeated(domain.ReasonNeverWatched, 20, "nw")...)

	got := applyDiscoveryQuota(ranked)

	firstPage := got[:24]
	unwatched := 0
	for _, v := range firstPage {
		if v.Reason == domain.ReasonNeverWatched {
			unwatched++
		}
	}
	// The 30% bucket over a 24-slot page is 7 entries.
	if unwatched < 7 {
		t.Fatalf("first page had %d never-watched videos, want at least 7", unwatched)
	}
}

func TestQuotaDropsNothing(t *testing.T) {
	var ranked []domain.RankedVideo
	ranked = append(ranked, repeated(domain.ReasonRewatch, 5, "rw")...)
	ranked = append(ranked, repeated(domain.ReasonNeverWatched, 3, "nw")...)
	ranked = append(ranked, repeated(domain.ReasonContinueWatching, 2, "cw")...)

	got := applyDiscoveryQuota(ranked)

	if len(got) != len(ranked) {
		t.Fatalf("got %d videos, want %d — the quota reorders, it never drops",
			len(got), len(ranked))
	}
	seen := map[string]bool{}
	for _, v := range got {
		if seen[v.VideoID] {
			t.Fatalf("video %s appeared twice", v.VideoID)
		}
		seen[v.VideoID] = true
	}
}

func TestQuotaFallsBackToScoreWhenABucketIsEmpty(t *testing.T) {
	// A brand-new user has nothing watched, so most buckets are empty.
	// The page must still fill, and no videos are dropped.
	ranked := repeated(domain.ReasonNeverWatched, 30, "nw")

	got := applyDiscoveryQuota(ranked)
	if len(got) != 30 {
		t.Fatalf("got %d, want 30", len(got))
	}
	// Shuffling within buckets means the first video is not guaranteed to be the
	// highest-scoring. But every video must still be present.
	seen := map[string]bool{}
	for _, v := range got {
		if seen[v.VideoID] {
			t.Fatalf("video %s appeared twice", v.VideoID)
		}
		seen[v.VideoID] = true
	}
	if len(seen) != 30 {
		t.Fatalf("only %d unique videos in the result", len(seen))
	}
}
