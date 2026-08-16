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

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())

	// 15% of the 72% adjustable share, over 24 slots, is 2 places.
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

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())

	assertSameSet(t, got, ranked)
}

func TestQuotaFallsBackToScoreWhenABucketIsEmpty(t *testing.T) {
	// A brand-new viewer has nothing watched, so most buckets are empty.
	// The page must still fill, and no videos are dropped.
	slots := map[string]feedSlot{}
	ranked := slotted(slotAffinity, 30, "af", slots)

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())

	assertSameSet(t, got, ranked)
}

func TestAVideoWithNoSlotStillReachesTheFeed(t *testing.T) {
	// Bounced videos belong to no adjustable share: they are neither a source
	// of new material nor a watch state anybody reserved room for.
	slots := map[string]feedSlot{}
	ranked := slotted(slotAffinity, 3, "af", slots)
	ranked = append(ranked, domain.RankedVideo{VideoID: "bounced", Score: 0.1})

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())

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
		FeedMix{Subscribed: 80, Affinity: 10, Discovery: 10}, defaultTuning())
	explorer := applyDiscoveryQuota(ranked, slots,
		FeedMix{Subscribed: 10, Affinity: 10, Discovery: 80}, defaultTuning())

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
		FeedMix{Subscribed: 100, Affinity: 0, Discovery: 0}, defaultTuning())

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

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())

	if n := countSlot(got[:24], slots, slotContinueWatching); n != 1 {
		t.Fatal("the one continue-watching video did not make the first page")
	}
}

func TestAnUnsetMixIsTheDefaultMix(t *testing.T) {
	// What a caller that has never heard of the setting sends. It must not be
	// read as "nothing at all", which is the one combination with no meaning.
	subscribed, affinity, discovery := FeedMix{}.normalised(shareFreshSubscribed)
	wantSub, wantAff, wantDis := DefaultFeedMix.normalised(shareFreshSubscribed)

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
	subscribed, affinity, discovery := small.normalised(shareFreshSubscribed)

	want := adjustableShare(shareFreshSubscribed)
	if total := subscribed + affinity + discovery; total < want-1e-9 ||
		total > want+1e-9 {
		t.Fatalf("shares summed to %.4f, want the adjustable share %.4f", total, want)
	}
	if subscribed <= affinity {
		t.Fatalf("3 against 2 came out as %.3f against %.3f", subscribed, affinity)
	}
}

// The bug that started this: the quota used to shuffle each bucket uniformly
// before taking its share, so the score decided nothing about the first page.
// A video that outscores its bucket by an order of magnitude — which is what a
// video published an hour ago on a followed channel is — landed in a uniformly
// random position among hundreds and reached the page about as often as any
// other. Everything the ranker computes runs through here.
func TestTheHighestScoringVideoUsuallyLeadsItsBucket(t *testing.T) {
	const trials = 200
	leads := 0
	for i := 0; i < trials; i++ {
		slots := map[string]feedSlot{}
		// One video far above the rest, then a long tail it has to beat.
		// slotted scores its first video at n, so the leader has to clear 200.
		ranked := []domain.RankedVideo{{VideoID: "top", Score: 230}}
		slots["top"] = slotAffinity
		ranked = append(ranked, slotted(slotAffinity, 200, "af", slots)...)

		got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())
		if got[0].VideoID == "top" {
			leads++
		}
	}
	// Uniform shuffle put it first about 1 time in 200. Sampling by score has to
	// be overwhelmingly more decisive than that to be worth the name.
	if leads < trials*9/10 {
		t.Fatalf("the top-scoring video led %d of %d pages; the score is not "+
			"deciding the order", leads, trials)
	}
}

// Sampling must not depend on how big the library is.
//
// The regression this exists for, measured on the real catalogue: with the whole
// bucket in the draw, eight of the first twenty-four videos scored below zero
// while two dozen above fourteen sat unused. Gumbel noise is unbounded and the
// largest of N draws grows like log N, so at four thousand candidates something
// worthless always draws its way to the front. The test above missed it because
// two hundred candidates is not four thousand — which is the whole lesson.
func TestALargeLibraryDoesNotLetWorthlessVideosOntoTheFirstPage(t *testing.T) {
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
	// The shape of the real thing: a few dozen good videos and thousands of
	// mediocre-to-bad ones, all in one bucket.
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("good%d", i)
		slots[id] = slotAffinity
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: 15 - float64(i)*0.02})
	}
	for i := 0; i < 4000; i++ {
		id := fmt.Sprintf("weak%d", i)
		slots[id] = slotAffinity
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: 2 - float64(i)*0.001})
	}
	sortRanked(ranked)

	for trial := 0; trial < 20; trial++ {
		got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())
		for i, v := range got[:24] {
			if v.Score < 0 {
				t.Fatalf("trial %d: position %d holds a video scoring %.2f while "+
					"forty above fourteen were available", trial, i, v.Score)
			}
		}
	}
}

// The other half of the same requirement. Freezing the order outright would fix
// the bug above and reintroduce the one the shuffle was there to prevent: a feed
// that looks identical every time it is opened.
func TestVideosWithEqualScoresDoNotAlwaysLandInTheSameOrder(t *testing.T) {
	orders := map[string]bool{}
	for i := 0; i < 100; i++ {
		slots := map[string]feedSlot{}
		var ranked []domain.RankedVideo
		for _, id := range []string{"a", "b", "c", "d", "e"} {
			slots[id] = slotAffinity
			ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: 5})
		}

		got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())
		var key string
		for _, v := range got {
			key += v.VideoID
		}
		orders[key] = true
	}
	if len(orders) < 2 {
		t.Fatal("five equally scored videos always came back in one order; " +
			"the feed would look the same on every refresh")
	}
}

// Sampling reorders; it must not lose or duplicate anything, and the guarantee
// applies to a bucket of one as much as to a bucket of a thousand.
func TestSamplingByScoreKeepsEveryVideo(t *testing.T) {
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
	ranked = append(ranked, slotted(slotAffinity, 50, "af", slots)...)
	ranked = append(ranked, slotted(slotDiscovery, 1, "dc", slots)...)

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())

	assertSameSet(t, got, ranked)
}

// Scores here are not bounded below — a bounced video from a disliked topic
// goes well under zero — and exp of a large negative must not become a zero
// weight that the draw cannot handle.
func TestSamplingSurvivesNegativeScores(t *testing.T) {
	slots := map[string]feedSlot{}
	ranked := []domain.RankedVideo{
		{VideoID: "worst", Score: -40},
		{VideoID: "bad", Score: -12},
		{VideoID: "best", Score: -1},
	}
	for _, v := range ranked {
		slots[v.VideoID] = slotAffinity
	}

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())

	assertSameSet(t, got, ranked)
}

// The reported symptom, stated as a test: a channel the viewer follows publishes,
// and the video has to be on the first screen. Not probably — the subscribed
// bucket is five places wide and a household with twenty followed channels will
// lose that race often enough to be noticed, which is what was noticed.
func TestAFreshVideoFromAFollowedChannelAlwaysReachesTheFirstPage(t *testing.T) {
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
	// A saturated feed: the subscribed bucket is already full of older uploads
	// that all outscore the new one.
	ranked = append(ranked, slotted(slotSubscribed, 100, "sub", slots)...)
	ranked = append(ranked, slotted(slotAffinity, 100, "af", slots)...)
	slots["justPublished"] = slotFreshSubscribed
	ranked = append(ranked, domain.RankedVideo{VideoID: "justPublished", Score: 0.1})

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())

	if countSlot(got[:24], slots, slotFreshSubscribed) != 1 {
		t.Fatal("a video published an hour ago on a followed channel did not make " +
			"the first page, which is the whole reason this slot exists")
	}
}

// The share is a reservation, not a hole in the page. Most of the time nothing
// is fresh, and on those days the place has to go to somebody else.
func TestTheFreshShareIsGivenBackWhenNothingIsFresh(t *testing.T) {
	slots := map[string]feedSlot{}
	ranked := slotted(slotAffinity, 50, "af", slots)

	got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, defaultTuning())

	if len(got) < 24 {
		t.Fatalf("page came back %d long; an unused reservation must not shorten it",
			len(got))
	}
	assertSameSet(t, got, ranked)
}

// A household following three channels that each post twice in a day should see
// all six. The per-channel cap is right for the rest of the feed and wrong here:
// it exists to stop a heavily watched channel becoming the page, and a channel
// that posted twice today is not doing that.
func TestTheChannelCapDoesNotApplyToFreshSubscribedVideos(t *testing.T) {
	slots := map[string]feedSlot{}
	channelOf := map[string]string{}
	var ranked []domain.RankedVideo
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("new%d", i)
		slots[id] = slotFreshSubscribed
		channelOf[id] = "ch_followed"
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(20 - i)})
	}
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("other%d", i)
		slots[id] = slotAffinity
		channelOf[id] = fmt.Sprintf("ch_%d", i)
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(10 - i)})
	}

	got := applyChannelDiversity(ranked, channelOf, slots, maxPerChannelPerWindow, quotaWindow)

	if n := countSlot(got[:24], slots, slotFreshSubscribed); n != 6 {
		t.Fatalf("%d of 6 new uploads from the followed channel reached the first "+
			"window; the cap must not apply to them", n)
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

// The gateway keeps its own copy of this default, deliberately: it does not
// link the ranker, and a REST layer reaching into another service's use cases
// for a constant is the dependency this architecture exists to prevent.
//
// Duplication is the price of that boundary, and drift is the bill. The two
// stood at 60/20/20 and 25/60/15 for a release — so "the default mix" had two
// answers, and which one a viewer got depended on whether the gateway happened
// to name a mix in the request. The value is asserted here so that changing one
// side without the other fails a test rather than a Saturday.
func TestTheDefaultMixMatchesTheOneTheGatewayPublishes(t *testing.T) {
	// services/gateway/internal/api/feed_mix.go — defaultFeedMix
	want := FeedMix{Subscribed: 60, Affinity: 20, Discovery: 20}
	if DefaultFeedMix != want {
		t.Fatalf("DefaultFeedMix = %+v, want %+v — update the gateway's copy too", DefaultFeedMix, want)
	}
}

// Three from one channel in twenty-four slots is a mixed page. The same three
// at positions 6, 7 and 9 is that channel's page.
//
// The cap could not tell those apart: it counted how many and never asked
// where. Measured on the real feed before the gap existed — one channel at
// positions 6, 7 and 9 of the first window, another at 3 and 4, a third at 13
// and 14, and the top channel taking its full three in every one of the first
// five windows.
func TestVideosFromOneChannelAreSpacedOut(t *testing.T) {
	slots := map[string]feedSlot{}
	channelOf := map[string]string{}
	var ranked []domain.RankedVideo

	// One channel holding the top scores, so left alone it would take the first
	// three slots outright.
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("loud%d", i)
		slots[id] = slotAffinity
		channelOf[id] = "ch_loud"
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(100 - i)})
	}
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("other%d", i)
		slots[id] = slotAffinity
		channelOf[id] = fmt.Sprintf("ch_%d", i)
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(50 - i)})
	}

	got := applyChannelDiversity(ranked, channelOf, slots, maxPerChannelPerWindow, quotaWindow)

	last := -1
	gap := channelGap(maxPerChannelPerWindow, quotaWindow)
	for i, v := range got[:quotaWindow] {
		if channelOf[v.VideoID] != "ch_loud" {
			continue
		}
		if last >= 0 && i-last < gap {
			t.Fatalf("two videos from one channel at %d and %d, want at least %d apart", last, i, gap)
		}
		last = i
	}
}

// The gap holds across the window boundary, which the count never did.
//
// `seen` resets at each window, so a channel could take its third slot at 23
// and its next at 24 — two adjacent videos from one channel, with both windows
// reporting themselves within the cap.
func TestTheGapSurvivesTheWindowBoundary(t *testing.T) {
	slots := map[string]feedSlot{}
	channelOf := map[string]string{}
	var ranked []domain.RankedVideo
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("loud%d", i)
		slots[id] = slotAffinity
		channelOf[id] = "ch_loud"
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(100 - i)})
	}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("other%d", i)
		slots[id] = slotAffinity
		channelOf[id] = fmt.Sprintf("ch_%d", i)
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(50 - i)})
	}

	got := applyChannelDiversity(ranked, channelOf, slots, maxPerChannelPerWindow, quotaWindow)

	gap := channelGap(maxPerChannelPerWindow, quotaWindow)
	last := -1
	for i, v := range got[:3*quotaWindow] {
		if channelOf[v.VideoID] != "ch_loud" {
			continue
		}
		if last >= 0 && i-last < gap {
			t.Fatalf("positions %d and %d are %d apart, want %d — the boundary let a run through",
				last, i, i-last, gap)
		}
		last = i
	}
}

// Nothing is dropped by the spacing, only moved.
func TestSpacingReordersAndNeverDrops(t *testing.T) {
	slots := map[string]feedSlot{}
	channelOf := map[string]string{}
	var ranked []domain.RankedVideo
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("v%d", i)
		slots[id] = slotAffinity
		channelOf[id] = "ch_one"
		ranked = append(ranked, domain.RankedVideo{VideoID: id, Score: float64(30 - i)})
	}
	got := applyChannelDiversity(ranked, channelOf, slots, maxPerChannelPerWindow, quotaWindow)
	assertSameSet(t, got, ranked)
}
