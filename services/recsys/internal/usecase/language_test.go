package usecase

import (
	"fmt"
	"testing"
	"time"

	"github.com/lucnguyen/local-youtube/services/recsys/internal/domain"
)

// A household that has never watched a word of Hindi should not be offered it.
//
// Measured before this existed: the feed carried Hindi, Malayalam, Indonesian
// and Nepali to a household whose whole history is 406 English videos, 200 of
// unknown language, 27 Vietnamese and 18 en-US — and Vietnamese was 11 of the
// first 1000 slots against Hindi's 21.
func TestALanguageNobodyWatchesIsNotOffered(t *testing.T) {
	var features []domain.VideoFeatures
	watched := map[string]float32{}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("en%d", i)
		features = append(features, domain.VideoFeatures{VideoID: id, ChannelID: "ch_en", Language: "en"})
		watched[id] = 1
	}
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("vi%d", i)
		features = append(features, domain.VideoFeatures{VideoID: id, ChannelID: "ch_vi", Language: "vi"})
		watched[id] = 1
	}

	langs := buildWatchedLanguages(features, watched)
	if !langs["en"] || !langs["vi"] {
		t.Fatalf("watched languages = %v, want en and vi", langs)
	}
	if langs["hi"] {
		t.Error("a language never watched was admitted")
	}
	if unreadable(domain.VideoFeatures{Language: "en-GB"}, langs) {
		t.Error("en-GB was treated as a different language from en")
	}
	if !unreadable(domain.VideoFeatures{Language: "hi"}, langs) {
		t.Error("Hindi passed a filter built from a history with none in it")
	}
}

// One accidental open must not admit a language for good.
//
// The column is filled from the title on flat listings, so a single row in a
// language is as likely to be a mis-tagged title as a real viewing.
func TestOneViewDoesNotAdmitALanguage(t *testing.T) {
	var features []domain.VideoFeatures
	watched := map[string]float32{}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("en%d", i)
		features = append(features, domain.VideoFeatures{VideoID: id, Language: "en"})
		watched[id] = 1
	}
	features = append(features, domain.VideoFeatures{VideoID: "once", Language: "ko"})
	watched["once"] = 1

	if buildWatchedLanguages(features, watched)["ko"] {
		t.Error("a single view admitted a language")
	}
}

// Unknown is always readable.
//
// A quarter of this library carries no language at all, and 200 of those have
// been watched. Excluding them to catch a handful of Bollywood would be the
// worst trade in the ranker.
func TestUnknownLanguageIsAlwaysAllowed(t *testing.T) {
	langs := map[string]bool{"en": true}
	if unreadable(domain.VideoFeatures{Language: ""}, langs) {
		t.Error("a video with no language recorded was excluded")
	}
}

// A new library shows everything until it has been taught something.
func TestTheRuleIsOffUntilThereIsAHistory(t *testing.T) {
	features := []domain.VideoFeatures{{VideoID: "a", Language: "hi"}}
	watched := map[string]float32{"a": 1}

	if langs := buildWatchedLanguages(features, watched); langs != nil {
		t.Errorf("a four-video history produced a filter: %v", langs)
	}
	if unreadable(features[0], nil) {
		t.Error("an empty language set must let everything through")
	}
}

// Subscribing is a deliberate choice and is not second-guessed.
func TestASubscribedChannelIsNeverFilteredByLanguage(t *testing.T) {
	now := time.Now()
	features := []domain.VideoFeatures{{
		VideoID: "bollywood", ChannelID: "ch_hi", Language: "hi",
		PublishedAt: now, AddedAt: now, MediaState: "MEDIA_STATE_READY",
	}}
	profile := emptyProfile()
	profile.Subscribed = map[string]bool{"ch_hi": true}

	in := rankInputs{
		profile:          profile,
		watchedLanguages: map[string]bool{"en": true},
		suppressed:       func(string) bool { return false },
		now:              now,
		tuning:           Tuning{}.resolve(),
	}
	if got := scoreVideo(features[0], in); got.Excluded == excludedUnreadable {
		t.Error("a video from a channel the viewer subscribed to was filtered by language")
	}
}

// Expansion's finds are discovery, never affinity.
//
// The affinity slot means "more of what you watch". A channel the viewer never
// chose cannot be more of anything — at best it resembles their taste — and
// letting it sit there gave uninvited material a fifth of the page under a name
// saying the viewer had asked for it. Discovery is where it belongs: bounded,
// reserved, and explicitly for the unfamiliar.
func TestExpansionMaterialIsDiscoveryNotAffinity(t *testing.T) {
	now := time.Now()
	base := domain.VideoFeatures{
		ChannelID: "ch_x", PublishedAt: now, AddedAt: now,
		MediaState: "MEDIA_STATE_READY",
	}
	in := rankInputs{
		profile:    emptyProfile(),
		suppressed: func(string) bool { return false },
		now:        now,
		tuning:     Tuning{}.resolve(),
		// High affinity for the channel, so without the provenance rule these
		// would land in the affinity slot.
		watchAffinity: WatchAffinity{
			Channels: map[string]float64{"ch_x": 1},
			Topics:   map[string]float64{},
		},
	}

	curated := base
	curated.VideoID = "curated"
	if got := scoreVideo(curated, in); got.Slot != slotAffinity {
		t.Errorf("a curated video with high affinity landed in %q, want the affinity slot", got.Slot)
	}

	found := base
	found.VideoID = "found"
	found.DiscoveredVia = "RELATED"
	if got := scoreVideo(found, in); got.Slot != slotDiscovery {
		t.Errorf("an expansion find landed in %q, want discovery", got.Slot)
	}
}

// Anything ingested before the column existed is treated as curated.
//
// Guessing retroactively is what the previous attempt at this got wrong: it
// read "no topic" as "arrived uninvited", until the metadata backfill filled
// YouTube's own category in for everything.
func TestVideosWithNoRecordedProvenanceAreTreatedAsCurated(t *testing.T) {
	now := time.Now()
	in := rankInputs{
		profile:    emptyProfile(),
		suppressed: func(string) bool { return false },
		now:        now,
		tuning:     Tuning{}.resolve(),
		watchAffinity: WatchAffinity{
			Channels: map[string]float64{"ch_x": 1},
			Topics:   map[string]float64{},
		},
	}
	legacy := domain.VideoFeatures{
		VideoID: "legacy", ChannelID: "ch_x", PublishedAt: now, AddedAt: now,
		MediaState: "MEDIA_STATE_READY",
	}
	if got := scoreVideo(legacy, in); got.Slot != slotAffinity {
		t.Errorf("a video with no provenance was demoted to %q", got.Slot)
	}
}

// YouTube's own home feed is fenced exactly as ExpandLibrary's finds are.
//
// It arrives through a household member's signed-in session, so it is not
// uninvited in the way a search result was — but it is still an ordering nobody
// here can account for, and §6's whole value is that every score can be. It is
// allowed to be material; it is not allowed to be a fifth of the page under a
// name saying the viewer asked for it.
func TestYouTubeRecommendationsAreDiscoveryNotAffinity(t *testing.T) {
	now := time.Now()
	in := rankInputs{
		profile:    emptyProfile(),
		suppressed: func(string) bool { return false },
		now:        now,
		tuning:     Tuning{}.resolve(),
		watchAffinity: WatchAffinity{
			Channels: map[string]float64{"ch_x": 1},
			Topics:   map[string]float64{},
		},
	}
	f := domain.VideoFeatures{
		VideoID: "guessed", ChannelID: "ch_x", PublishedAt: now, AddedAt: now,
		MediaState: "MEDIA_STATE_READY", DiscoveredVia: "YOUTUBE_REC",
	}
	if got := scoreVideo(f, in); got.Slot != slotDiscovery {
		t.Errorf("a YouTube recommendation landed in %q, want discovery", got.Slot)
	}
}
