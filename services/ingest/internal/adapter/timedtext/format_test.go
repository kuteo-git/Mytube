package timedtext

import (
	"net/url"
	"strings"
	"testing"
)

// The format is replaced, never appended.
//
// This is the whole of a bug that produced silence rather than an error. Track
// URLs in a player response already carry `fmt=srv3`; appending `&fmt=vtt`
// leaves two of them, and YouTube takes the first. Measured on a real URL:
// appended answered `<?xml …><timedtext format="3">`, replaced answered
// `WEBVTT`.
//
// What reached the disk was an 81 KB file named `.vtt` holding XML. Everything
// downstream behaved correctly and the result was nothing: zero cues parsed, a
// subtitle track listed that showed nothing, and "no subtitles available" over a
// file that was plainly there.
func TestWithFormatReplacesRatherThanAppends(t *testing.T) {
	const base = "https://www.youtube.com/api/timedtext?v=abc&lang=en&fmt=srv3&caps=asr"

	got, err := withFormat(base, "vtt")
	if err != nil {
		t.Fatalf("withFormat: %v", err)
	}

	q, err := url.ParseQuery(strings.SplitN(got, "?", 2)[1])
	if err != nil {
		t.Fatalf("parsing the result: %v", err)
	}
	if len(q["fmt"]) != 1 {
		t.Fatalf("fmt appears %d times: %v", len(q["fmt"]), q["fmt"])
	}
	if q.Get("fmt") != "vtt" {
		t.Errorf("fmt = %q, want vtt", q.Get("fmt"))
	}
	// Everything else in the query is a signed part of the address and must
	// survive untouched, or the request is refused rather than answered wrongly.
	for _, keep := range []string{"v", "lang", "caps"} {
		if q.Get(keep) == "" {
			t.Errorf("withFormat dropped %q", keep)
		}
	}
}

// A URL with no format at all gains one.
func TestWithFormatAddsWhenAbsent(t *testing.T) {
	got, err := withFormat("https://www.youtube.com/api/timedtext?v=abc", "vtt")
	if err != nil {
		t.Fatalf("withFormat: %v", err)
	}
	if !strings.Contains(got, "fmt=vtt") {
		t.Errorf("got %q, want fmt=vtt in it", got)
	}
}
