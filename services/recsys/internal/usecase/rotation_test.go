package usecase

import (
	"math"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

func rotationFixture(now time.Time) []domain.VideoFeatures {
	return []domain.VideoFeatures{
		{VideoID: "hot1", ChannelID: "hot", DurationSeconds: 600},
		{VideoID: "hot2", ChannelID: "hot", DurationSeconds: 600},
		{VideoID: "cold1", ChannelID: "cold", DurationSeconds: 600},
		{VideoID: "never1", ChannelID: "never", DurationSeconds: 600},
	}
}

// A channel watched today stands down; one left alone comes back.
//
// This is the whole of what was missing. Measured before it existed: three
// channels held 38 of the first 120 slots, and 55 of 85 subscribed channels had
// nothing before position 240 — not excluded, just outranked for ever by
// whichever channels had been watched the most.
func TestAWatchedChannelCoolsAndAQuietOneReturns(t *testing.T) {
	now := time.Now()
	profile := domain.UserProfile{
		WatchedFraction: map[string]float32{"hot1": 1, "cold1": 1},
		WatchedAt: map[string]time.Time{
			"hot1":  now.Add(-2 * time.Hour),
			"cold1": now.Add(-40 * 24 * time.Hour),
		},
		RecentImpressions: map[string]bool{},
		ImpressionCounts:  map[string]int{},
	}

	r := buildChannelRotation(rotationFixture(now), profile, now)

	if r.FatigueFor("hot") <= r.FatigueFor("cold") {
		t.Errorf("a channel watched two hours ago is not hotter than one watched six weeks ago: %v vs %v",
			r.FatigueFor("hot"), r.FatigueFor("cold"))
	}
	if r.RevivalFor("cold", now) <= r.RevivalFor("hot", now) {
		t.Errorf("the quiet channel is owed more than the one just watched: %v vs %v",
			r.RevivalFor("cold", now), r.RevivalFor("hot", now))
	}
}

// Seven days, because that is what was asked for.
func TestHeatHalvesInAWeek(t *testing.T) {
	now := time.Now()
	fresh := halfLifeDecay(0, channelHeatHalfLifeDays)
	week := halfLifeDecay(7*24*time.Hour, channelHeatHalfLifeDays)
	fortnight := halfLifeDecay(14*24*time.Hour, channelHeatHalfLifeDays)

	if math.Abs(fresh-1) > 1e-9 {
		t.Errorf("today = %v, want 1", fresh)
	}
	if math.Abs(week-0.5) > 1e-9 {
		t.Errorf("after a week = %v, want 0.5", week)
	}
	if math.Abs(fortnight-0.25) > 1e-9 {
		t.Errorf("after a fortnight = %v, want 0.25", fortnight)
	}
	_ = now
}

// A channel never watched is owed nothing.
//
// Revival lifts things back out of the past. Something never seen has no past
// here, and giving it one would take the discovery share's job and do it
// without the discovery share's limits.
func TestAChannelNeverWatchedGetsNoRevival(t *testing.T) {
	now := time.Now()
	profile := domain.UserProfile{
		WatchedFraction:   map[string]float32{},
		WatchedAt:         map[string]time.Time{},
		RecentImpressions: map[string]bool{},
		ImpressionCounts:  map[string]int{},
	}
	r := buildChannelRotation(rotationFixture(now), profile, now)
	if r.RevivalFor("never", now) != 0 {
		t.Errorf("revival = %v for a channel never watched, want 0", r.RevivalFor("never", now))
	}
}

// Being shown counts as heat, at less than being watched.
//
// Without it a channel offered on every page and never opened stays cold for
// ever and keeps being offered, which is most of what "the feed is boring"
// describes.
func TestBeingShownMakesAChannelWarmer(t *testing.T) {
	now := time.Now()
	features := rotationFixture(now)
	base := domain.UserProfile{
		WatchedFraction:   map[string]float32{},
		WatchedAt:         map[string]time.Time{},
		RecentImpressions: map[string]bool{},
		ImpressionCounts:  map[string]int{},
	}
	shown := domain.UserProfile{
		WatchedFraction:   map[string]float32{},
		WatchedAt:         map[string]time.Time{},
		RecentImpressions: map[string]bool{"never1": true},
		ImpressionCounts:  map[string]int{},
	}

	if buildChannelRotation(features, base, now).FatigueFor("never") != 0 {
		t.Error("a channel neither watched nor shown has heat")
	}
	if buildChannelRotation(features, shown, now).FatigueFor("never") <= 0 {
		t.Error("a channel shown repeatedly never cools down the feed")
	}
}

// The offer that keeps being declined.
//
// Nothing in this service has ever seen a thumbnail. This is as close as it
// gets: shown eight times and never opened is an answer about the thumbnail and
// the title, whatever the topic score says.
func TestAVideoOfferedAndRefusedIsPushedDown(t *testing.T) {
	profile := domain.UserProfile{
		WatchedFraction: map[string]float32{"watched": 0.9},
		ImpressionCounts: map[string]int{
			"once":    1,
			"thrice":  3,
			"often":   8,
			"endless": 100,
			"watched": 40,
		},
	}

	if p := ignoredPenalty("once", profile); p != 0 {
		t.Errorf("one showing already counts against a video: %v", p)
	}
	if p := ignoredPenalty("thrice", profile); p != 0 {
		t.Errorf("at the floor the count should still say nothing: %v", p)
	}
	if ignoredPenalty("often", profile) <= 0 {
		t.Error("eight refusals cost a video nothing")
	}
	if p := ignoredPenalty("endless", profile); p != maxIgnoredPenalty {
		t.Errorf("penalty = %v, want it capped at %v", p, maxIgnoredPenalty)
	}
	// A video someone opened has not been declined, however often it was
	// offered first.
	if p := ignoredPenalty("watched", profile); p != 0 {
		t.Errorf("a watched video was penalised for having been offered: %v", p)
	}
}

// Watch time counts, and not linearly.
func TestTimeSpentCountsWithoutRunningAwayWithIt(t *testing.T) {
	minute := watchTimeWeight(60)
	hour := watchTimeWeight(3600)

	if hour <= minute {
		t.Errorf("an hour (%v) must count for more than a minute (%v)", hour, minute)
	}
	if hour > 3*minute {
		t.Errorf("an hour (%v) counts more than three times a minute (%v) — this is meant to be logarithmic", hour, minute)
	}
	// A video with no known duration must not erase a real viewing.
	if watchTimeWeight(0) != 1 {
		t.Errorf("unknown duration = %v, want a neutral 1", watchTimeWeight(0))
	}
}

// Watch affinity fades, so a feed cannot stay frozen around last spring.
func TestWatchAffinityFadesWithAge(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{
		{VideoID: "recent", ChannelID: "a", DurationSeconds: 600},
		{VideoID: "ancient", ChannelID: "b", DurationSeconds: 600},
	}
	watched := map[string]float32{"recent": 1, "ancient": 1}
	at := map[string]time.Time{
		"recent":  now.Add(-24 * time.Hour),
		"ancient": now.Add(-365 * 24 * time.Hour),
	}

	a := buildWatchAffinity(features, watched, at, now)
	if a.Channels["b"] >= a.Channels["a"] {
		t.Errorf("a year-old viewing argues as loudly as yesterday's: %v vs %v",
			a.Channels["b"], a.Channels["a"])
	}
}

// Reactions can no longer outgrow the rest of the ranker.
func TestReactionScoreIsBounded(t *testing.T) {
	if got := squashReaction(30); got >= 1 {
		t.Errorf("thirty likes scored %v, want it under 1", got)
	}
	if squashReaction(2) <= squashReaction(1) {
		t.Error("bounding must not flatten two likes into one")
	}
	if squashReaction(0) != 0 || squashReaction(-1) != 0 {
		t.Error("nothing reacted to must score nothing")
	}
}
