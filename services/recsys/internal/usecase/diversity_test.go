package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// dominatedLibrary builds a library where one channel would win every slot on
// score alone, which is the situation this cap exists for.
func dominatedLibrary(dominant, others int) ([]domain.VideoFeatures, map[string]float32) {
	now := time.Now()
	features := make([]domain.VideoFeatures, 0, dominant+others)
	watched := map[string]float32{}

	for i := 0; i < dominant; i++ {
		id := "dom_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		features = append(features, domain.VideoFeatures{
			VideoID: id, ChannelID: "ch_dominant", Topics: []string{"Music"}, AddedAt: now,
		})
	}
	// Watch history concentrated on the dominant channel, which is what drives
	// its affinity to 1.0.
	for i := 0; i < dominant/2; i++ {
		watched["seen_"+string(rune('a'+i%26))] = 1.0
	}
	for i := 0; i < dominant/2; i++ {
		features = append(features, domain.VideoFeatures{
			VideoID:   "seen_" + string(rune('a'+i%26)),
			ChannelID: "ch_dominant", Topics: []string{"Music"}, AddedAt: now,
		})
	}

	for i := 0; i < others; i++ {
		id := "other_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		features = append(features, domain.VideoFeatures{
			VideoID: id, ChannelID: "ch_" + string(rune('a'+i%20)), AddedAt: now,
		})
	}
	return features, watched
}

func TestOneChannelCannotOwnThePage(t *testing.T) {
	// Measured before this existed: 44% of watch time on one channel produced a
	// front page that was 23 of 24 videos from it, while every reason quota was
	// nominally satisfied.
	features, watched := dominatedLibrary(60, 60)
	ranker := NewRanker(
		stubStore{profile: profileWith(watched)},
		stubFeatures{features: features},
	)

	ranked, err := ranker.rankAll(context.Background(), "viewer", "")
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	if len(ranked) < quotaWindow {
		t.Fatalf("expected at least a full window, got %d", len(ranked))
	}

	counts := map[string]int{}
	for _, v := range ranked[:quotaWindow] {
		for _, f := range features {
			if f.VideoID == v.VideoID {
				counts[f.ChannelID]++
				break
			}
		}
	}
	if counts["ch_dominant"] > maxPerChannelPerWindow {
		t.Fatalf("the dominant channel took %d of the first %d slots, cap is %d",
			counts["ch_dominant"], quotaWindow, maxPerChannelPerWindow)
	}
	if len(counts) < 5 {
		t.Fatalf("first page drew on only %d channels", len(counts))
	}
}

func TestChannelDiversityReordersAndNeverDrops(t *testing.T) {
	// The same contract the reason quota holds to: a video pushed out of one
	// window has to appear in a later one, or heavy watching would make parts
	// of the library unreachable by scrolling.
	ranked := make([]domain.RankedVideo, 0, 40)
	channelOf := map[string]string{}
	for i := 0; i < 40; i++ {
		id := "v" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(40 - i)})
		channelOf[id] = "ch_one"
	}

	out := applyChannelDiversity(ranked, channelOf)

	if len(out) != len(ranked) {
		t.Fatalf("got %d videos back from %d", len(out), len(ranked))
	}
	seen := map[string]bool{}
	for _, v := range out {
		if seen[v.VideoID] {
			t.Fatalf("video %q appears twice", v.VideoID)
		}
		seen[v.VideoID] = true
	}
	for _, v := range ranked {
		if !seen[v.VideoID] {
			t.Fatalf("video %q was dropped", v.VideoID)
		}
	}
}

func TestChannelDiversityLeavesAnAlreadyVariedPageAlone(t *testing.T) {
	// The cap must be invisible when it is not needed, or it becomes a second
	// ranking signal nobody asked for.
	ranked := make([]domain.RankedVideo, 0, 24)
	channelOf := map[string]string{}
	for i := 0; i < 24; i++ {
		id := "v" + string(rune('a'+i))
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(24 - i)})
		channelOf[id] = "ch_" + string(rune('a'+i))
	}

	out := applyChannelDiversity(ranked, channelOf)
	for i := range ranked {
		if out[i].VideoID != ranked[i].VideoID {
			t.Fatalf("position %d changed from %q to %q with no channel repeated",
				i, ranked[i].VideoID, out[i].VideoID)
		}
	}
}

func TestVideosWithNoKnownChannelAreNeverHeldBack(t *testing.T) {
	// An unknown channel cannot crowd anything out, so treating the empty
	// string as a channel would bunch unrelated videos together and penalise
	// them for having incomplete metadata.
	ranked := make([]domain.RankedVideo, 0, 10)
	channelOf := map[string]string{}
	for i := 0; i < 10; i++ {
		id := "v" + string(rune('a'+i))
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(10 - i)})
		channelOf[id] = ""
	}

	out := applyChannelDiversity(ranked, channelOf)
	for i := range ranked {
		if out[i].VideoID != ranked[i].VideoID {
			t.Fatalf("a video with no known channel was reordered at position %d", i)
		}
	}
}
