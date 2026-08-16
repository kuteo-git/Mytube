package usecase

import (
	"fmt"
	"testing"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// A share set to zero must remove those videos from the feed, not move them to
// the end of it.
//
// They used to be appended after everything else, on the reasoning that the
// quota has never dropped a video. That reasoning holds for a bucket that ran
// out; it does not hold for one somebody switched off. On the real household
// member with a modest subscribed pool, the subscribed videos were exhausted —
// and then reordered behind by the channel-diversity pass, which appends what
// it holds back — so the discovery pool that had been set to zero began on the
// second page. A slider at the bottom that still produces videos is a control
// that lies about what it does.
func TestZeroShareReallyMeansNone(t *testing.T) {
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
	add := func(prefix string, slot feedSlot, n int, score float64) {
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("%s%d", prefix, i)
			slots[id] = slot
			ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: score - float64(i)*0.001})
		}
	}
	// Mirrors the real household member: a modest subscribed pool and a huge
	// discovery pool that scores higher, which is what makes the fallback
	// visible.
	add("sub", slotSubscribed, 626, 5)
	add("fresh", slotFreshSubscribed, 126, 9)
	add("aff", slotAffinity, 219, 50)
	add("disc", slotDiscovery, 5235, 50)

	out := applyDiscoveryQuota(ranked, slots, FeedMix{Subscribed: 100}, Tuning{}.resolve())

	for _, v := range out {
		if s := slots[v.VideoID]; s == slotAffinity || s == slotDiscovery {
			t.Fatalf("%s came from a share set to zero", v.VideoID)
		}
	}
	if len(out) != 626+126 {
		t.Errorf("kept %d videos, want %d", len(out), 626+126)
	}
}

// The same, reached the way the viewer reaches it: through the diversity pass,
// which appends held-back videos and so used to land them behind the suppressed
// ones.
func TestZeroShareSurvivesChannelDiversity(t *testing.T) {
	slots := map[string]feedSlot{}
	channelOf := map[string]string{}
	var ranked []domain.RankedVideo
	add := func(prefix string, slot feedSlot, n, channels int, score float64) {
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("%s%d", prefix, i)
			slots[id] = slot
			channelOf[id] = fmt.Sprintf("ch_%s_%d", prefix, i%channels)
			ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: score - float64(i)*0.001})
		}
	}
	// Nineteen followed channels against a library built by somebody else: the
	// subscribed pool is both smaller and concentrated, so diversity holds a lot
	// of it back.
	add("sub", slotSubscribed, 626, 19, 5)
	add("disc", slotDiscovery, 5235, 700, 50)

	out := applyDiscoveryQuota(ranked, slots, FeedMix{Subscribed: 100}, Tuning{}.resolve())
	out = applyChannelDiversity(out, channelOf, slots, maxPerChannelPerWindow, quotaWindow)

	for _, v := range out {
		if slots[v.VideoID] == slotDiscovery {
			t.Fatalf("%s came from a share set to zero", v.VideoID)
		}
	}
}
