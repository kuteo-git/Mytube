package ytdlp

import (
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

// Which heights belong on the ladder.
//
// Two rules meeting in one predicate, each a decision rather than an accident of
// what YouTube publishes:
//
//   - The ceiling is 2160. It governs bytes that are streamed and never stored,
//     so the disk budget has nothing to say about it.
//   - The floor is 240, because §7 cuts 144p for good. An automatic ladder
//     reaches anything it can reach, so a rung never worth watching has to be
//     absent rather than merely last.
func TestUsableRenditionHeight(t *testing.T) {
	const ceiling = 2160

	cases := []struct {
		name   string
		height int32
		want   bool
	}{
		{"4K, the ceiling itself", 2160, true},
		{"2K", 1440, true},
		{"1080p", 1080, true},
		{"720p", 720, true},
		{"480p", 480, true},
		{"360p", 360, true},
		{"the floor itself", 240, true},
		{"144p is not offered at all", 144, false},
		{"nor anything below it", 108, false},
		{"above the ceiling", 4320, false},
		// yt-dlp reports no height on some formats, and zero must not read as a
		// rung that is merely unusual — it is not a rung.
		{"no height reported", 0, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := usableRenditionHeight(c.height, ceiling); got != c.want {
				t.Errorf("usableRenditionHeight(%d, %d) = %v, want %v",
					c.height, ceiling, got, c.want)
			}
		})
	}
}

// A phone's ceiling really does exclude the rungs above it.
func TestUsableRenditionHeightUnderAPhonesCeiling(t *testing.T) {
	for _, h := range []int32{240, 480, 720} {
		if !usableRenditionHeight(h, 720) {
			t.Errorf("height %d refused under a ceiling of 720", h)
		}
	}
	for _, h := range []int32{1080, 1440, 2160} {
		if usableRenditionHeight(h, 720) {
			t.Errorf("height %d accepted under a ceiling of 720", h)
		}
	}
}

// Which of two encodings of the same height wins.
//
// The rule was "the highest bitrate", written when every candidate at a height
// was H.264 — two encodings of one codec, where more bits is more picture. That
// stopped being true when the ceiling rose past 1080p: at 1080p YouTube
// publishes both avc1 and av01, and on a measured video avc1 carries 3358k
// against av01's 1619k *for the same picture*. Comparing bitrates across codecs
// is not a quality comparison at all.
func TestH264WinsAtAHeightThatPublishesBoth(t *testing.T) {
	avc := domain.MediaTrack{Codec: "avc1.640028", Height: 1080, Bitrate: 3358000}
	av1 := domain.MediaTrack{Codec: "av01.0.08M.08", Height: 1080, Bitrate: 1619000}

	// Whichever arrives first, H.264 is what the ladder keeps — so 1080p and
	// below are exactly what they were before AV1 was allowed in at all.
	if preferRendition(avc, av1) {
		t.Error("AV1 displaced H.264 at 1080p")
	}
	if !preferRendition(av1, avc) {
		t.Error("H.264 did not displace AV1 at 1080p")
	}
}

// And where H.264 does not exist, AV1 is taken rather than nothing.
//
// This is the whole reason it is allowed in: measured on a real 4K upload,
// 1440p and 2160p are published as vp9 and av01 and nothing else.
func TestAV1IsTakenWhereThereIsNoH264(t *testing.T) {
	low := domain.MediaTrack{Codec: "av01.0.12M.08", Height: 2160, Bitrate: 9910000}
	high := domain.MediaTrack{Codec: "av01.0.12M.08", Height: 2160, Bitrate: 18906000}

	// Same codec, so bitrate decides again — the original rule, now scoped to
	// where it is meaningful.
	if !preferRendition(low, high) {
		t.Error("the better encoding of the same codec was refused")
	}
}

func TestLadderDepth(t *testing.T) {
	// 240 · 360 · 480 · 720 · 1080 · 1440 · 2160.
	if maxRenditions != 7 {
		t.Fatalf("maxRenditions = %d, want 7", maxRenditions)
	}
	if minRenditionHeight != 240 {
		t.Fatalf("minRenditionHeight = %d, want 240", minRenditionHeight)
	}
}
