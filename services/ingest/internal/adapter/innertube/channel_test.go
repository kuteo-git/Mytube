package innertube

import (
	"testing"
	"time"
)

func TestParseCompactCount(t *testing.T) {
	cases := map[string]int64{
		"1.5M views":   1_500_000,
		"40M views":    40_000_000,
		"12,345 views": 12345,
		"938K views":   938_000,
		"1.2B views":   1_200_000_000,
		"No views":     0,
		"":             0,
		"21 hours ago": 21, // not a count; callers only pass view strings here
	}
	for input, want := range cases {
		if got := parseCompactCount(input); got != want {
			t.Errorf("parseCompactCount(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestParseRelativeTime(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)

	cases := map[string]time.Time{
		"21 hours ago":  now.Add(-21 * time.Hour),
		"3 days ago":    now.AddDate(0, 0, -3),
		"2 weeks ago":   now.AddDate(0, 0, -14),
		"5 months ago":  now.AddDate(0, -5, 0),
		"7 years ago":   now.AddDate(-7, 0, 0),
		"1 minute ago":  now.Add(-time.Minute),
		"Streamed live": {},
		"":              {},
	}
	for input, want := range cases {
		got := parseRelativeTime(input, now)
		if !got.Equal(want) {
			t.Errorf("parseRelativeTime(%q) = %v, want %v", input, got, want)
		}
	}
}

// An unreadable phrase must produce the zero time, not "now" — a video dated
// now renders as "1 minute ago" on every card, which is a plausible-looking lie
// and worse than showing nothing (see CLAUDE.md §8b traps).
func TestUnparseableDateIsZeroNotNow(t *testing.T) {
	if got := parseRelativeTime("sometime recently", time.Now()); !got.IsZero() {
		t.Fatalf("got %v, want zero time", got)
	}
}
