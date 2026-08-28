package httpapi

import (
	"testing"

	"github.com/lucnguyen/local-youtube/services/ingest/internal/domain"
)

func ladder() []domain.MediaTrack {
	return []domain.MediaTrack{
		{Height: 2160}, {Height: 1440}, {Height: 1080},
		{Height: 720}, {Height: 480}, {Height: 360}, {Height: 240},
	}
}

func heights(v []domain.MediaTrack) []int {
	out := make([]int, 0, len(v))
	for _, t := range v {
		out = append(out, t.Height)
	}
	return out
}

// A phone is capped by writing a shorter master playlist, not by anything in the
// browser.
//
// That is not a preference. On an iPhone HLS plays natively and a page has no
// way to pin or limit a level — Safari picks from whatever ladder it is handed —
// so the only place the ceiling can be enforced is here.
func TestCappedRenditions(t *testing.T) {
	if got := heights(cappedRenditions(ladder(), 720)); len(got) != 4 || got[0] != 720 {
		t.Errorf("a phone was offered %v, want 720 and below", got)
	}
	if got := heights(cappedRenditions(ladder(), 0)); len(got) != 7 {
		t.Errorf("no cap gave %v, want the whole ladder", got)
	}
}

// A cap below everything this video publishes gives the whole ladder back.
//
// A picture larger than the screen is a waste; no picture at all is a fault, and
// only one of those is worth preventing.
func TestACapBelowEveryRungOffersThemAnyway(t *testing.T) {
	only4K := []domain.MediaTrack{{Height: 2160}}
	if got := heights(cappedRenditions(only4K, 720)); len(got) != 1 {
		t.Errorf("got %v, want the video's only rung rather than nothing", got)
	}
}

// What the query parameter accepts, and what it refuses to a stranger on the LAN.
func TestParseMaxHeight(t *testing.T) {
	if got := parseMaxHeight("720"); got != 720 {
		t.Errorf("parseMaxHeight(\"720\") = %d", got)
	}
	// Absent is what a desktop sends, and it means no cap.
	for _, raw := range []string{"", "abc", "0", "-1", "99999"} {
		if got := parseMaxHeight(raw); got != 0 {
			t.Errorf("parseMaxHeight(%q) = %d, want no cap", raw, got)
		}
	}
}
