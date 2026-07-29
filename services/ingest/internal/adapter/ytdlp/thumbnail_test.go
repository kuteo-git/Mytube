package ytdlp

import (
	"testing"

	"github.com/lrstanley/go-ytdlp"
)

func thumb(url string, width int) *ytdlp.ExtractedThumbnail {
	w := width
	return &ytdlp.ExtractedThumbnail{URL: url, Width: &w}
}

func TestWidestThumbnailPrefersTheLargest(t *testing.T) {
	// The single "thumbnail" field yt-dlp exposes is hqdefault at 480×360, and
	// taking it made every card visibly soft: a three-column grid gives each
	// one about 560 points, twice that on a retina screen. The array carries
	// maxresdefault at 1920.
	got := widestThumbnail([]*ytdlp.ExtractedThumbnail{
		thumb("https://i.ytimg.com/vi/x/hqdefault.jpg", 480),
		thumb("https://i.ytimg.com/vi/x/maxresdefault.jpg", 1920),
		thumb("https://i.ytimg.com/vi/x/sddefault.jpg", 640),
	})
	if got != "https://i.ytimg.com/vi/x/maxresdefault.jpg" {
		t.Fatalf("picked %q", got)
	}
}

func TestWidestThumbnailPrefersJPEGAtEqualWidth(t *testing.T) {
	// Not an aesthetic preference: this is meant to reach a television browser,
	// and WebP is the format those are least likely to decode — the same
	// reasoning that picks h264 over AV1 for the video itself.
	got := widestThumbnail([]*ytdlp.ExtractedThumbnail{
		thumb("https://i.ytimg.com/vi_webp/x/maxresdefault.webp", 1920),
		thumb("https://i.ytimg.com/vi/x/maxresdefault.jpg", 1920),
	})
	if got != "https://i.ytimg.com/vi/x/maxresdefault.jpg" {
		t.Fatalf("picked %q; WebP won a tie it should lose", got)
	}
}

func TestWidestThumbnailStillAnswersWhenNoWidthsAreReported(t *testing.T) {
	// Flat listings often omit dimensions. Skipping unmeasured entries would
	// leave a video with no image at all rather than an unverified one.
	got := widestThumbnail([]*ytdlp.ExtractedThumbnail{
		{URL: "https://i.ytimg.com/vi/x/default.jpg"},
		{URL: "https://i.ytimg.com/vi/x/hqdefault.jpg"},
	})
	if got == "" {
		t.Fatal("no thumbnail chosen from entries without dimensions")
	}
}

func TestWidestThumbnailIgnoresEmptyEntries(t *testing.T) {
	got := widestThumbnail([]*ytdlp.ExtractedThumbnail{
		nil,
		{URL: ""},
		thumb("https://i.ytimg.com/vi/x/hqdefault.jpg", 480),
	})
	if got != "https://i.ytimg.com/vi/x/hqdefault.jpg" {
		t.Fatalf("picked %q", got)
	}
}

func TestWidestThumbnailOfNothingIsEmpty(t *testing.T) {
	if got := widestThumbnail(nil); got != "" {
		t.Fatalf("picked %q from an empty list", got)
	}
}
