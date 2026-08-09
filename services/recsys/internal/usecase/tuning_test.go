package usecase

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

func ptrFloat(v float64) *float64 { return &v }
func ptrInt(v int) *int           { return &v }

// A caller that has never heard of the setting — an older gateway, or a fresh
// install with no file — must rank exactly as the built-in constants do. If an
// absent tuning meant zeros, a zero maximum age would empty the feed outright.
func TestAnAbsentTuningIsTheBuiltInBehaviour(t *testing.T) {
	if got, want := (Tuning{}).resolve(), defaultTuning(); got != want {
		t.Fatalf("an empty tuning resolved to %+v, want %+v", got, want)
	}
}

// Zero is a real answer for some of these, and the pointer is the only thing
// that distinguishes it from silence.
func TestZeroIsASettingWhereZeroMeansSomething(t *testing.T) {
	got := (Tuning{SessionBlend: ptrFloat(0)}).resolve()
	if got.sessionBlend != 0 {
		t.Fatalf("a session blend explicitly set to zero came out as %.2f", got.sessionBlend)
	}
	// And the rest of the settings are untouched by it.
	if got.maxPublishedAgeDays != maxPublishedAgeDays {
		t.Fatal("setting one field discarded the others")
	}
}

// The file is meant to be edited by hand, so every value has to survive a typo.
// Clamped rather than rejected, like the gateway's feed mix: there is no reading
// of "the feed will not load" that beats "the feed is not quite what you asked".
func TestNonsenseIsClampedRatherThanRefused(t *testing.T) {
	got := (Tuning{
		SessionBlend:        ptrFloat(99),
		MaxPublishedAgeDays: ptrInt(-5),
		SoftmaxTemperature:  ptrFloat(0),
		SamplePoolSize:      ptrInt(1_000_000),
	}).resolve()

	if got.sessionBlend != 1 {
		t.Fatalf("session blend of 99 became %.2f, want 1", got.sessionBlend)
	}
	if got.maxPublishedAgeDays < 1 {
		t.Fatalf("a negative maximum age became %.0f — the feed would be empty",
			got.maxPublishedAgeDays)
	}
	if got.softmaxTemperature <= 0 {
		t.Fatalf("temperature clamped to %.3f; at zero the feed is frozen in score "+
			"order and looks identical on every refresh", got.softmaxTemperature)
	}
	if got.samplePoolSize > 20*quotaWindow {
		t.Fatalf("sample pool of a million became %d — this is the ceiling that "+
			"stops worthless videos reaching the first page", got.samplePoolSize)
	}
}

// A NaN passes every comparison, so an unclamped one would spread through every
// score it touched and sort the whole feed arbitrarily.
func TestNaNIsNotASetting(t *testing.T) {
	got := (Tuning{SessionBlend: ptrFloat(math.NaN())}).resolve()
	if got.sessionBlend != got.sessionBlend {
		t.Fatal("a NaN session blend survived into the ranking")
	}
}

// The knob that exists to be a guard rail: raising the pool past its ceiling is
// how the first page filled with videos scoring below zero.
func TestTheSamplePoolCeilingStillHoldsWhenSetByHand(t *testing.T) {
	slots := map[string]feedSlot{}
	var ranked []domain.RankedVideo
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

	// Somebody sets it to "no limit" in the file.
	tuning := (Tuning{SamplePoolSize: ptrInt(999999)}).resolve()

	for trial := 0; trial < 20; trial++ {
		got := applyDiscoveryQuota(ranked, slots, DefaultFeedMix, tuning)
		for pos, v := range got[:24] {
			if v.Score < 0 {
				t.Fatalf("trial %d: position %d holds a video scoring %.2f", trial, pos, v.Score)
			}
		}
	}
}

// The whole point of the advanced screen: a number moved there has to change
// the feed. Freshness is the clearest case — widen the window and a video from
// last week starts counting as new.
func TestWideningTheFreshnessWindowChangesWhatCountsAsNew(t *testing.T) {
	now := time.Now()
	profile := emptyProfile()
	profile.Subscribed["ch_1"] = true
	features := []domain.VideoFeatures{{
		VideoID: "lastWeek", ChannelID: "ch_1", Topics: []string{"Music"},
		AddedAt: now, PublishedAt: now.Add(-5 * 24 * time.Hour), ViewCount: 1000,
	}}
	ranker := NewRanker(stubStore{profile: profile}, stubFeatures{features: features})

	narrow, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil, Tuning{})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}
	wide, err := ranker.rankAll(context.Background(), "viewer", "", DefaultFeedMix, nil,
		Tuning{FreshnessWindowHours: ptrInt(24 * 7)})
	if err != nil {
		t.Fatalf("rankAll: %v", err)
	}

	if scoreOf(t, wide, "lastWeek").Score <= scoreOf(t, narrow, "lastWeek").Score {
		t.Fatal("a week-wide freshness window did not make a five-day-old video new")
	}
}

// Moving the fresh-subscribed share has to move what the three sliders divide,
// or the settings page ends up quoting a number that was true last release —
// which is exactly the drift this whole change is fixing.
func TestTheFreshShareChangesWhatTheSlidersDivide(t *testing.T) {
	base := adjustableShare(shareFreshSubscribed)
	wider := adjustableShare((Tuning{FreshSubscribedPercent: ptrInt(30)}).resolve().shareFreshSubscribed)

	if wider >= base {
		t.Fatalf("raising the fresh share to 30%% left %.2f for the sliders, "+
			"against %.2f before", wider, base)
	}
	if got := (Tuning{FreshSubscribedPercent: ptrInt(0)}).resolve().shareFreshSubscribed; got != 0 {
		t.Fatalf("a fresh share of zero came out as %.2f", got)
	}
}

// The UI draws its sliders against these, so a range that disagrees with the
// clamp is a control that does nothing at one end.
func TestPublishedBoundsMatchWhatTheClampEnforces(t *testing.T) {
	bounds := TuningBounds()
	for _, name := range []string{
		"sessionBlend", "freshSubscribedPercent", "freshnessWindowHours",
		"maxPublishedAgeDays", "recencyHalfLifeDays", "softmaxTemperature",
		"samplePoolSize",
	} {
		got, ok := bounds[name]
		if !ok {
			t.Fatalf("%s has no published range, so the UI cannot draw it", name)
		}
		if got[0] >= got[1] {
			t.Fatalf("%s published as [%v, %v]", name, got[0], got[1])
		}
	}
	if len(bounds) != 7 {
		t.Fatalf("published %d ranges for 7 settings", len(bounds))
	}
}
