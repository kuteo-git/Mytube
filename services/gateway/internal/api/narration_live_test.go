package api

import (
	"testing"
	"time"
)

// The playlist below is copied from Al Jazeera English's live caption feed
// while it was on air, with the segment addresses shortened. Its shape is the
// whole of the timing: a date for the first segment, a sequence number, and
// five seconds each.
const liveCaptionPlaylist = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:5
#EXT-X-MEDIA-SEQUENCE:616251
#EXT-X-DISCONTINUITY-SEQUENCE:138
#EXT-X-PROGRAM-DATE-TIME:2026-09-03T11:55:35.032+00:00
#EXTINF:5.0,
https://example.invalid/sq/616251/captions.webvtt
#EXTINF:5.0,
https://example.invalid/sq/616252/captions.webvtt
#EXTINF:5.0,
https://example.invalid/sq/616253/captions.webvtt
`

func TestParseLivePlaylistPutsEachSegmentOnTheClock(t *testing.T) {
	segs := parseLivePlaylist(liveCaptionPlaylist)
	if len(segs) != 3 {
		t.Fatalf("segments: got %d, want 3", len(segs))
	}

	first, err := time.Parse(time.RFC3339, "2026-09-03T11:55:35.032+00:00")
	if err != nil {
		t.Fatal(err)
	}
	for i, seg := range segs {
		wantSeq := 616251 + i
		if seg.sequence != wantSeq {
			t.Errorf("segment %d sequence: got %d, want %d", i, seg.sequence, wantSeq)
		}
		// The date belongs to the first segment; each later one is its own
		// EXTINF further on.
		want := first.Add(time.Duration(i) * 5 * time.Second)
		if !seg.at.Equal(want) {
			t.Errorf("segment %d at: got %s, want %s", i, seg.at, want)
		}
	}
}

func TestParseLivePlaylistRefusesOneWithNoDate(t *testing.T) {
	// Without a date there is no clock, and a clip placed at a guessed time is
	// worse than one that never plays.
	raw := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:5.0,\nhttps://example.invalid/a\n"
	if got := parseLivePlaylist(raw); got != nil {
		t.Fatalf("no date: got %d segments, want none", len(got))
	}
}

// The two segments below are copied from the same broadcast. The second opens
// with the words the first ended on, which is how HLS publishes a cue that
// spans a segment boundary — split, not revised.
const (
	liveSegmentOne = `WEBVTT
X-TIMESTAMP-MAP=LOCAL:00:00.000,MPEGTS:2532795056

00:00:00.000 --> 00:00:03.000
picture picture uh from uh
Iranian national media that

00:00:03.000 --> 00:00:05.000
shows the aftermath of
`
	liveSegmentTwo = `WEBVTT
X-TIMESTAMP-MAP=LOCAL:00:00.000,MPEGTS:2533245056

00:00:00.000 --> 00:00:01.000
shows the aftermath of

00:00:01.000 --> 00:00:04.000
uh the bombing that hit the
wedding house. Uh and currently
`
)

func TestFeedJoinsACueSplitAcrossSegments(t *testing.T) {
	feed := &liveCaptionFeed{}
	base := time.Date(2026, 9, 3, 11, 55, 35, 0, time.UTC)

	feed.absorb(base, liveSegmentOne)
	out := feed.absorb(base.Add(5*time.Second), liveSegmentTwo)

	if len(out) != 1 {
		t.Fatalf("clauses: got %d, want 1", len(out))
	}
	// "shows the aftermath of" is published in both segments. Said twice, a
	// listener hears a stutter; this is the join that stops it.
	if got := countOccurrences(out[0].text, "shows the aftermath of"); got != 1 {
		t.Errorf("the split cue appears %d times in %q, want once", got, out[0].text)
	}
	// The clause begins where *its own first words* were spoken. Those are
	// "picture picture uh from uh…" at the very start of the first segment —
	// not the split cue, and not the segment that finally closed the clause.
	// Getting this wrong puts a spoken line seconds away from the picture it
	// belongs to.
	want := base
	if out[0].at.Sub(want).Abs() > 100*time.Millisecond {
		t.Errorf("clause at %s, want about %s", out[0].at.UTC(), want)
	}
}

func TestFeedWaitsForPunctuationBeforeClosingAClause(t *testing.T) {
	feed := &liveCaptionFeed{}
	base := time.Date(2026, 9, 3, 11, 55, 35, 0, time.UTC)

	// A fragment with no boundary in it closes nothing: "shows the aftermath
	// of" is not a sentence, and translating it alone is what machine
	// translation handles worst.
	if out := feed.absorb(base, liveSegmentOne); len(out) != 0 {
		t.Fatalf("a fragment closed %d clauses, want none", len(out))
	}
}

func countOccurrences(haystack, needle string) int {
	n, at := 0, 0
	for {
		i := indexFrom(haystack, needle, at)
		if i < 0 {
			return n
		}
		n++
		at = i + 1
	}
}

func indexFrom(haystack, needle string, from int) int {
	if from >= len(haystack) {
		return -1
	}
	rest := haystack[from:]
	for i := 0; i+len(needle) <= len(rest); i++ {
		if rest[i:i+len(needle)] == needle {
			return from + i
		}
	}
	return -1
}
