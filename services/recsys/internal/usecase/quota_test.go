package usecase

import (
	"fmt"
	"testing"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// slotted builds n videos in one slot, highest score first, and records which
// slot they are in. The quota is told the slot separately from the reason —
// see applyDiscoveryQuota — so a test has to say both.
func slotted(
	slot feedSlot,
	n int,
	prefix string,
	slots map[string]feedSlot,
) []domain.RankedVideo {
	out := make([]domain.RankedVideo, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%d", prefix, i)
		slots[id] = slot
		out = append(out, domain.RankedVideo{VideoID: id, Score: float64(n - i)})
	}
	return out
}

func countSlot(videos []domain.RankedVideo, slots map[string]feedSlot, want feedSlot) int {
	n := 0
	for _, v := range videos {
		if slots[v.VideoID] == want {
			n++
		}
	}
	return n
}

// The failure this prevents: likes and subscriptions both pull the feed toward
// the familiar, and a pure score ordering under both collapses to one subject.
func TestQuotaKeepsUnfamiliarVideosInTheFirstPageEvenWhenTheyScoreLowest(t *testing.T) {
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
	// Twenty high-scoring rewatches, then discoveries scoring below them.
	ranked = append(ranked, slotted(slotRewatch, 20, "rw", slots)...)
	ranked = append(ranked, slotted(slotDiscovery, 20, "dc", slots)...)

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix)

	// 15% of the 82% adjustable share, over 24 slots, is 2 places.
	if n := countSlot(got[:24], slots, slotDiscovery); n < 2 {
		t.Fatalf("first page had %d discovery videos, want at least 2", n)
	}
}

func TestQuotaDropsNothing(t *testing.T) {
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
	ranked = append(ranked, slotted(slotRewatch, 5, "rw", slots)...)
	ranked = append(ranked, slotted(slotAffinity, 3, "af", slots)...)
	ranked = append(ranked, slotted(slotContinueWatching, 2, "cw", slots)...)

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix)

	assertSameSet(t, got, ranked)
}

func TestQuotaFallsBackToScoreWhenABucketIsEmpty(t *testing.T) {
	// A brand-new viewer has nothing watched, so most buckets are empty.
	// The page must still fill, and no videos are dropped.
	slots := map[string]feedSlot{}
	ranked := slotted(slotAffinity, 30, "af", slots)

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix)

	assertSameSet(t, got, ranked)
}

func TestAVideoWithNoSlotStillReachesTheFeed(t *testing.T) {
	// Bounced videos belong to no adjustable share: they are neither a source
	// of new material nor a watch state anybody reserved room for.
	slots := map[string]feedSlot{}
	ranked := slotted(slotAffinity, 3, "af", slots)
	ranked = append(ranked, domain.RankedVideo{VideoID: "bounced", Score: 0.1})

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix)

	assertSameSet(t, got, ranked)
}

func TestTheMixDecidesHowMuchOfThePageEachSourceGets(t *testing.T) {
	// The point of the whole setting: the same ranking, two mixes, two feeds.
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
	ranked = append(ranked, slotted(slotSubscribed, 40, "sub", slots)...)
	ranked = append(ranked, slotted(slotAffinity, 40, "af", slots)...)
	ranked = append(ranked, slotted(slotDiscovery, 40, "dc", slots)...)

	subscriberHeavy := applyDiscoveryQuota(ranked, slots,
		FeedMix{Subscribed: 80, Affinity: 10, Discovery: 10})
	explorer := applyDiscoveryQuota(ranked, slots,
		FeedMix{Subscribed: 10, Affinity: 10, Discovery: 80})

	subs := countSlot(subscriberHeavy[:24], slots, slotSubscribed)
	explorerSubs := countSlot(explorer[:24], slots, slotSubscribed)
	if subs <= explorerSubs {
		t.Fatalf("subscriber-heavy first page had %d subscribed videos against %d for "+
			"the explorer's; the slider did nothing", subs, explorerSubs)
	}
	if got := countSlot(explorer[:24], slots, slotDiscovery); got < 10 {
		t.Fatalf("an 80%% discovery mix put %d discovery videos on the first page", got)
	}
}

func TestZeroMeansNoneOfThatOnThePage(t *testing.T) {
	// A slider dragged to the bottom that still produces videos is a control
	// that lies. The old floor gave every bucket one place per window whatever
	// the share was — right for a bucket nobody chose, wrong for a zero somebody
	// set on purpose.
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
	ranked = append(ranked, slotted(slotSubscribed, 30, "sub", slots)...)
	ranked = append(ranked, slotted(slotDiscovery, 30, "dc", slots)...)

	got := applyDiscoveryQuota(ranked, slots,
		FeedMix{Subscribed: 100, Affinity: 0, Discovery: 0})

	if n := countSlot(got[:24], slots, slotDiscovery); n != 0 {
		t.Fatalf("discovery set to zero still put %d videos on the first page", n)
	}
	// Suppressed, not deleted: scrolling far enough must still reach them, or
	// the quota has started dropping videos, which it never has.
	assertSameSet(t, got, ranked)
}

func TestTheFixedSharesKeepTheirFloor(t *testing.T) {
	// Continue-watching and rewatch are not adjustable, so the reason the floor
	// exists still applies to them: a thinly stocked bucket must not be starved
	// by a page's worth of something else.
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
	ranked = append(ranked, slotted(slotAffinity, 100, "af", slots)...)
	ranked = append(ranked, slotted(slotContinueWatching, 1, "cw", slots)...)

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix)

	if n := countSlot(got[:24], slots, slotContinueWatching); n != 1 {
		t.Fatal("the one continue-watching video did not make the first page")
	}
}

func TestAnUnsetMixIsTheDefaultMix(t *testing.T) {
	// What a caller that has never heard of the setting sends. It must not be
	// read as "nothing at all", which is the one combination with no meaning.
	subscribed, affinity, discovery := FeedMix{}.normalised()
	wantSub, wantAff, wantDis := DefaultFeedMix.normalised()

	if subscribed != wantSub || affinity != wantAff || discovery != wantDis {
		t.Fatalf("an empty mix normalised to %.3f/%.3f/%.3f, want the defaults %.3f/%.3f/%.3f",
			subscribed, affinity, discovery, wantSub, wantAff, wantDis)
	}
}

func TestTheMixIsReadAsARatio(t *testing.T) {
	// 3/2/1 and 50/33/17 describe the same feed. Percentages that do not add to
	// a hundred are arithmetic the UI already does; the service honours the
	// proportions rather than arguing.
	small := FeedMix{Subscribed: 3, Affinity: 2, Discovery: 1}
	subscribed, affinity, discovery := small.normalised()

	if total := subscribed + affinity + discovery; total < shareAdjustable-1e-9 ||
		total > shareAdjustable+1e-9 {
		t.Fatalf("shares summed to %.4f, want the adjustable share %.4f",
			total, shareAdjustable)
	}
	if subscribed <= affinity {
		t.Fatalf("3 against 2 came out as %.3f against %.3f", subscribed, affinity)
	}
}

func assertSameSet(t *testing.T, got, want []domain.RankedVideo) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d videos, want %d — the quota reorders, it never drops",
			len(got), len(want))
	}
	seen := map[string]bool{}
	for _, v := range got {
		if seen[v.VideoID] {
			t.Fatalf("video %s appeared twice", v.VideoID)
		}
		seen[v.VideoID] = true
	}
	for _, v := range want {
		if !seen[v.VideoID] {
			t.Fatalf("video %s went missing", v.VideoID)
		}
	}
}
