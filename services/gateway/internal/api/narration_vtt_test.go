package api

import (
	"math"
	"strings"
	"testing"
)

// The cases here are the ones the TypeScript parser's own suite keeps, ported
// alongside the parser. They are not a fresh set of guesses: each one is a shape
// that broke the original, and a Go port that passes its own invented examples
// while failing these would be a port in name only.

func TestParseVTTTime(t *testing.T) {
	if got := parseVTTTime("00:01:23.456"); math.Abs(got-83.456) > 1e-9 {
		t.Errorf("00:01:23.456 = %v, want 83.456", got)
	}
	if got := parseVTTTime("01:00:00.000"); got != 3600 {
		t.Errorf("01:00:00.000 = %v, want 3600", got)
	}
	if got := parseVTTTime("nonsense"); !math.IsNaN(got) {
		t.Errorf("nonsense = %v, want NaN", got)
	}
}

func TestFirstClauseBoundary(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{
			// The first, not the last. Taking the last swallows every boundary
			// before it and returns one cue where the rule asks for three.
			name: "takes the first boundary",
			text: "Hello there my friend. How are you",
			want: len([]rune("Hello there my friend.")),
		},
		{"ignores a decimal point", "it costs 2.5 dollars", -1},
		{"ignores common abbreviations", "ask Dr. Smith about it", -1},
		{
			// "Then," alone would be translated with no sentence around it and
			// voiced as a clip half a second long.
			name: "ignores a comma with too little on one side",
			text: "Then, how are you",
			want: -1,
		},
		{
			name: "takes a comma with enough on both sides",
			text: "wait a moment, I will be right there",
			want: len([]rune("wait a moment,")),
		},
		{"finds a boundary at the very end", "all done.", len([]rune("all done."))},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := firstClauseBoundary(c.text); got != c.want {
				t.Errorf("firstClauseBoundary(%q) = %d, want %d", c.text, got, c.want)
			}
		})
	}
}

// The offsets are rune offsets, and this is the one thing the port had to get
// right that TypeScript got for free. Counting bytes would put the cut several
// characters early on any line with a Vietnamese vowel in it — and the library
// this runs against is full of them.
func TestFirstClauseBoundaryCountsRunes(t *testing.T) {
	text := "Chào bạn hiền. Hôm nay thế nào"
	want := len([]rune("Chào bạn hiền."))
	if got := firstClauseBoundary(text); got != want {
		t.Errorf("boundary = %d, want %d (bytes would give %d)",
			got, want, len("Chào bạn hiền."))
	}
}

func TestPayloadRunningIntoTheNextTimingLine(t *testing.T) {
	// No blank separator. Stepping over the following timing line after reading
	// a payload loses the cue that line introduced.
	raw := strings.Join([]string{
		"WEBVTT",
		"",
		"00:00:01.000 --> 00:00:02.000",
		"first line",
		"00:00:03.000 --> 00:00:04.000",
		"second line",
		"",
	}, "\n")

	cues := parseVTT(raw, "en")
	if len(cues) != 2 || cues[0].Text != "first line" || cues[1].Text != "second line" {
		t.Fatalf("got %+v, want two cues: first line, second line", cues)
	}
}

func TestReadsOnlyTheNewLineOfARollingCue(t *testing.T) {
	raw := strings.Join([]string{
		"WEBVTT",
		"",
		"00:00:00.000 --> 00:00:02.000",
		" ",
		"hello<00:00:00.500><c> there</c>",
		"",
		"00:00:02.000 --> 00:00:04.000",
		"hello there",
		"friend<00:00:02.500><c> of</c><00:00:03.000><c> mine.</c>",
		"",
	}, "\n")

	cues := parseVTT(raw, "en")
	var texts []string
	for _, c := range cues {
		texts = append(texts, c.Text)
	}
	all := strings.Join(texts, " ")
	if all != "hello there friend of mine." {
		t.Errorf("got %q, want %q", all, "hello there friend of mine.")
	}
	// Said once, not twice. Reading the repeated line is what makes a phrase
	// say itself again.
	if n := strings.Count(all, "hello"); n != 1 {
		t.Errorf("hello appears %d times, want 1", n)
	}
}

func TestTrailingClauseKeepsItsOwnTiming(t *testing.T) {
	// The remainder after a split used to inherit the end of the whole buffer,
	// claiming a moment later than the words it contains.
	raw := strings.Join([]string{
		"WEBVTT",
		"",
		"00:00:00.000 --> 00:00:06.000",
		"one<00:00:01.000><c> two.</c><00:00:02.000><c> three</c><00:00:03.000><c> four</c>",
		"",
	}, "\n")

	cues := parseVTT(raw, "en")
	if len(cues) != 2 {
		t.Fatalf("got %d cues, want 2: %+v", len(cues), cues)
	}
	if cues[0].Text != "one two." {
		t.Errorf("first cue = %q, want %q", cues[0].Text, "one two.")
	}
	if cues[1].Text != "three four" {
		t.Errorf("second cue = %q, want %q", cues[1].Text, "three four")
	}
	// It starts when "three" is spoken, not after "four".
	if math.Abs(cues[1].Start-2) > 1e-6 {
		t.Errorf("second cue starts at %v, want 2", cues[1].Start)
	}
}

func TestSnapshotCuesAreDropped(t *testing.T) {
	// YouTube writes a ~10ms clean copy of a line a real cue already carries.
	raw := strings.Join([]string{
		"WEBVTT",
		"",
		"00:00:01.000 --> 00:00:01.010",
		"a snapshot",
		"",
		"00:00:01.000 --> 00:00:03.000",
		"the real line",
		"",
	}, "\n")

	cues := parseVTT(raw, "en")
	if len(cues) != 1 || cues[0].Text != "the real line" {
		t.Fatalf("got %+v, want only the real line", cues)
	}
}

func TestStripBracketsKeepsEmotionTags(t *testing.T) {
	// The synthesiser understands these; other bracketed descriptions would be
	// read aloud as words.
	got := stripBrackets("xin chào [cười] các bạn [tiếng nhạc]")
	if !strings.Contains(got, "[cười]") {
		t.Errorf("%q lost its emotion tag", got)
	}
	if strings.Contains(got, "tiếng nhạc") {
		t.Errorf("%q kept a sound effect", got)
	}
}

func TestCleanCueTextDecodesEntitiesBeforeStripping(t *testing.T) {
	// Entities first, so &gt;&gt; is >> in time to be recognised as the speaker
	// marker it is rather than left in the text.
	if got := cleanCueText("&gt;&gt; hello &amp; goodbye"); got != "hello & goodbye" {
		t.Errorf("got %q, want %q", got, "hello & goodbye")
	}
}

// An unknown language is left in whole cues rather than cut on rules that do not
// describe its punctuation.
func TestUnknownLanguageIsNotSplit(t *testing.T) {
	raw := strings.Join([]string{
		"WEBVTT",
		"",
		"00:00:00.000 --> 00:00:04.000",
		"one<00:00:01.000><c> two.</c><00:00:02.000><c> three</c>",
		"",
	}, "\n")

	if cues := parseVTT(raw, "ja"); len(cues) != 3 {
		t.Fatalf("got %d cues, want 3 unsplit pieces: %+v", len(cues), cues)
	}
}
