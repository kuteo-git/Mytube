package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnUnsavedMixIsTheDefault(t *testing.T) {
	// Installing the setting must not change anybody's feed. If the defaults
	// differed from the quota this replaced, a viewer could not tell a slider
	// working from a default changing.
	if got := loadFeedMix(filepath.Join(t.TempDir(), "nope.json")); got != defaultFeedMix {
		t.Fatalf("missing file gave %+v, want the defaults %+v", got, defaultFeedMix)
	}
}

func TestFeedMixRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed-mix.json")
	in := feedMix{Subscribed: 50, Affinity: 30, Discovery: 20}
	if err := saveFeedMix(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := loadFeedMix(path); got != in {
		t.Fatalf("round trip gave %+v, want %+v", got, in)
	}
}

func TestAnUnreadableMixFileFallsBackRatherThanFailing(t *testing.T) {
	// This decides the order of a grid. There is no version of "the feed will
	// not load" that beats "the feed looks like it always did".
	path := filepath.Join(t.TempDir(), "feed-mix.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadFeedMix(path); got != defaultFeedMix {
		t.Fatalf("corrupt file gave %+v, want the defaults", got)
	}
}

func TestAMixOfNothingIsRejected(t *testing.T) {
	// Every other combination has a reading. All three at zero asks for a page
	// with no new material on it, which is an empty grid rather than a taste.
	if (feedMix{}).valid() {
		t.Error("all zero was accepted")
	}
	if (feedMix{Subscribed: -5, Affinity: 60, Discovery: 15}).valid() {
		t.Error("a negative share was accepted")
	}
	if !(feedMix{Subscribed: 100}).valid() {
		t.Error("only-subscribed is a legitimate way to watch and must be allowed")
	}
	if !(feedMix{Subscribed: 3, Affinity: 2, Discovery: 1}).valid() {
		t.Error("shares are read as a ratio, so they need not add to a hundred")
	}
}

func TestASavedMixOfNothingIsIgnoredOnLoad(t *testing.T) {
	// The endpoint refuses it, but a file edited by hand is not the endpoint.
	path := filepath.Join(t.TempDir(), "feed-mix.json")
	if err := os.WriteFile(path,
		[]byte(`{"subscribedPercent":0,"affinityPercent":0,"discoveryPercent":0}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadFeedMix(path); got != defaultFeedMix {
		t.Fatalf("an empty mix on disk gave %+v, want the defaults", got)
	}
}
