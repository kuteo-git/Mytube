package rpc

import (
	"testing"
	"time"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/services/catalog/internal/domain"
)

// Every field the ranker reads has to survive the crossing.
//
// Written after `is_short` did not. The column was added, the query selected
// it, the repository filled the domain struct and the recsys client read it off
// the wire — and this mapping in the middle never copied it, so recsys saw
// false for every video and Shorts stayed in the feed. Nothing failed: no
// error, no log line, and a field that is absent from proto3 JSON when false
// looks exactly like a field nobody set.
//
// Asserting on the struct field by field rather than on one value, because the
// next field added here will be dropped the same way.
func TestEveryFeatureFieldCrossesToProto(t *testing.T) {
	published := time.Date(2026, 8, 12, 3, 5, 47, 0, time.UTC)
	added := time.Date(2026, 8, 12, 12, 20, 5, 0, time.UTC)

	f := domain.VideoFeatures{
		VideoID:         "_1JtnBlDplo",
		ChannelID:       "UCupvZG",
		Topics:          []string{"People & Blogs"},
		Hashtags:        []string{"tag"},
		PublishedAt:     published,
		AddedAt:         added,
		DurationSeconds: 90,
		MediaState:      domain.MediaQueued,
		Language:        "en",
		ViewCount:       94389,
		IsShort:         true,
	}

	got := featuresToProto([]domain.VideoFeatures{f})
	if len(got) != 1 {
		t.Fatalf("mapped %d features, want 1", len(got))
	}
	v := got[0]

	checks := []struct {
		name string
		ok   bool
	}{
		{"video_id", v.GetVideoId() == f.VideoID},
		{"channel_id", v.GetChannelId() == f.ChannelID},
		{"topics", len(v.GetTopics()) == 1 && v.GetTopics()[0] == f.Topics[0]},
		{"hashtags", len(v.GetHashtags()) == 1 && v.GetHashtags()[0] == f.Hashtags[0]},
		{"published_at", v.GetPublishedAt().AsTime().Equal(published)},
		{"added_at", v.GetAddedAt().AsTime().Equal(added)},
		{"duration_seconds", v.GetDurationSeconds() == f.DurationSeconds},
		{"media_state", v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_QUEUED},
		{"language", v.GetLanguage() == f.Language},
		{"view_count", v.GetViewCount() == f.ViewCount},
		{"is_short", v.GetIsShort() == f.IsShort},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s did not survive the mapping", c.name)
		}
	}
}

// A video nobody has asked about crosses as false rather than as anything else.
func TestAnUnaskedVideoCrossesAsNotAShort(t *testing.T) {
	got := featuresToProto([]domain.VideoFeatures{{VideoID: "v"}})
	if got[0].GetIsShort() {
		t.Error("an unasked video crossed as a Short")
	}
}
