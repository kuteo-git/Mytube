package ytdlp

import "testing"

// A refusal and an absence want opposite responses: one is worth asking about
// again, the other is finished. Getting this wrong in the generous direction
// costs three full extracts every time somebody presses play on a video that
// simply has no captions — the counted kind of request, against the address
// CLAUDE.md §8 risk 6 is about.
func TestIsRateLimited(t *testing.T) {
	refusals := []string{
		"ERROR: Unable to download video subtitles for 'vi': HTTP Error 429: Too Many Requests",
		"HTTP Error 429",
		"too many requests",
		"YouTube said: rate limit exceeded",
	}
	for _, msg := range refusals {
		if !isRateLimited(msg) {
			t.Errorf("isRateLimited(%q) = false, want true", msg)
		}
	}

	// Everything else, including the two ordinary outcomes: no captions at all,
	// and a video upstream will not hand over for good.
	others := []string{
		"",
		"tgjYMym_0-c has no subtitles",
		"ERROR: [youtube] xyz: Video unavailable",
		"ERROR: unable to open for writing: no such file or directory",
	}
	for _, msg := range others {
		if isRateLimited(msg) {
			t.Errorf("isRateLimited(%q) = true, want false", msg)
		}
	}
}

func TestTailKeepsTheReason(t *testing.T) {
	// yt-dlp puts the reason at the end, so a truncated message must keep the
	// end rather than the banner it starts with.
	if got := tail("0123456789", 4); got != "6789" {
		t.Errorf("tail = %q, want %q", got, "6789")
	}
	if got := tail("short", 40); got != "short" {
		t.Errorf("tail = %q, want %q", got, "short")
	}
}
