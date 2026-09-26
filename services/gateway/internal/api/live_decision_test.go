package api

import "testing"

func TestDecideLive(t *testing.T) {
	for _, tc := range []struct {
		name       string
		liveStatus string
		isLiveNow  bool
		want       liveDecision
	}{
		// The reported bug: a station on air for hours, last checked 14 hours
		// ago, so the freshness window had expired.
		{"stale is_live", "is_live", false, liveAsk},
		{"fresh is_live", "is_live", true, liveYes},
		// A finished broadcast whose row nobody has corrected yet reads exactly
		// like the one above, which is why the answer is "ask" rather than
		// "yes" — only a resolve can tell the two apart.
		{"was_live", "was_live", false, liveNo},
		{"post_live", "post_live", false, liveNo},
		// Answered by the branch of its own, further down the route.
		{"is_upcoming", "is_upcoming", false, liveNo},
		{"not_live", "not_live", false, liveNo},
		// Nobody has ever asked. Not a reason to run yt-dlp on the play path.
		{"unknown", "", false, liveNo},
	} {
		if got := decideLive(tc.liveStatus, tc.isLiveNow); got != tc.want {
			t.Errorf("%s: decideLive(%q, %v) = %d, want %d",
				tc.name, tc.liveStatus, tc.isLiveNow, got, tc.want)
		}
	}
}
