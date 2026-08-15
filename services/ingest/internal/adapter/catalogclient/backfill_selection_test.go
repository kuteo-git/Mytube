package catalogclient

import (
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

func feature(id string, topics []string, dated bool, duration int32) *catalogv1.VideoFeatures {
	f := &catalogv1.VideoFeatures{VideoId: id, Topics: topics, DurationSeconds: duration}
	if dated {
		f.PublishedAt = timestamppb.Now()
	}
	return f
}

func ids(refs []domain.VideoRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.VideoID)
	}
	return out
}

// A missing date and a missing topic are not worth the same.
//
// An undated video is excluded from the feed outright; a video with no topic
// still appears, only ranked more weakly. The pass took whichever came first,
// and topic-only gaps outnumbered date gaps 4798 to 1123 — so a 200-video pass
// spent most of itself on the cheaper problem. Measured: 43 videos updated and
// 4 dates filled.
func TestTheBackfillTakesUndatedVideosFirst(t *testing.T) {
	videos := []*catalogv1.VideoFeatures{
		feature("topic_only_1", nil, true, 100),
		feature("topic_only_2", nil, true, 100),
		feature("undated", []string{"Music"}, false, 100),
		feature("topic_only_3", nil, true, 100),
	}

	refs := selectBackfillRefs(videos, 2)

	if got := ids(refs); len(got) != 2 || got[0] != "undated" {
		t.Fatalf("selected %v, want the undated video first", got)
	}
}

// A video whose duration was erased is invisible in a different way: the card
// reads 0:00. Nothing selected on it, so those never came back.
func TestAVideoMissingOnlyItsDurationIsSelected(t *testing.T) {
	videos := []*catalogv1.VideoFeatures{
		feature("complete", []string{"Music"}, true, 240),
		feature("zero_length", []string{"Music"}, true, 0),
	}

	refs := selectBackfillRefs(videos, 0)

	if got := ids(refs); len(got) != 1 || got[0] != "zero_length" {
		t.Fatalf("selected %v, want only zero_length", got)
	}
	if !refs[0].MissingDuration {
		t.Fatal("the ref does not say the duration is what is missing")
	}
}

// A video with everything is not worth a full metadata fetch, and those fetches
// are what this address has been blocked for making too many of.
func TestACompleteVideoIsNotSelected(t *testing.T) {
	videos := []*catalogv1.VideoFeatures{feature("complete", []string{"Music"}, true, 240)}

	if refs := selectBackfillRefs(videos, 0); len(refs) != 0 {
		t.Fatalf("selected %v, want nothing", ids(refs))
	}
}

// The limit still bounds the pass. Prioritising must not turn a bounded run
// into a walk of the whole library.
func TestTheLimitStillBoundsTheSelection(t *testing.T) {
	var videos []*catalogv1.VideoFeatures
	for i := 0; i < 50; i++ {
		videos = append(videos, feature(string(rune('a'+i%26))+string(rune('a'+i/26)), nil, true, 100))
	}

	if refs := selectBackfillRefs(videos, 10); len(refs) != 10 {
		t.Fatalf("selected %d refs, want 10", len(refs))
	}
}
