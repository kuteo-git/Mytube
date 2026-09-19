package api

import (
	"math"
	"reflect"
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
			// Three words is the same fault one word is, and measuring said so:
			// this clause is 0.80s of speech. minClauseBefore is five.
			name: "ignores a comma that closes too short a clause",
			text: "wait a moment, I will be right there",
			want: -1,
		},
		{
			name: "takes a comma with enough on both sides",
			text: "wait here for a moment, I will be right there",
			want: len([]rune("wait here for a moment,")),
		},
		{
			// A list separator. What follows this comma is one word, so the
			// clause it opens cannot stand alone however long the sentence is.
			name: "ignores a comma inside an enumeration",
			text: "if you have got the AirPods 4, 3, or 2, here is how they compare",
			want: len([]rune("if you have got the AirPods 4, 3, or 2,")),
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

// A cue that is nothing but a bracketed description carries a timestamp and no
// speech. Copied from 8OPCWER_68U, where "[âm nhạc]" runs from 6.55 to 11.43 and
// the greeting after it begins at 11.44: the clause the two of them formed took
// the bracket's start, so the voice spoke the greeting five seconds before the
// picture said it.
func TestParseVTTIgnoresATimestampWithNoSpeech(t *testing.T) {
	raw := strings.Join([]string{
		"WEBVTT",
		"",
		"00:00:06.550 --> 00:00:11.430 align:start position:0%",
		" ",
		"[âm nhạc]",
		"",
		"00:00:11.440 --> 00:00:12.709 align:start position:0%",
		" ",
		"Họ<00:00:11.639><c> đang</c><00:00:11.719><c> kính</c><00:00:11.840><c> chào</c><00:00:11.960><c> quý</c><00:00:12.120><c> vị.</c>",
		"",
	}, "\n")

	cues := parseVTT(raw, "vi")
	if len(cues) == 0 {
		t.Fatalf("no cues")
	}
	if math.Abs(cues[0].Start-11.44) > 1e-9 {
		t.Errorf("first cue starts at %v, want 11.44 (the bracket's 6.55 is not speech)", cues[0].Start)
	}
}

// The reported fault: cues cut mid-phrase instead of at punctuation.
//
// Every sentence here came off one measured video (kXVt4atqMv8) and each one
// broke in a different place, so they are kept whole rather than reduced to the
// rule each happens to exercise.
func TestClausesBreakAtPunctuationNotMidPhrase(t *testing.T) {
	cases := []struct {
		name     string
		sentence string
		want     []string
	}{
		{
			// Split at the list comma this left "3, or 2." alone, 0.72s long.
			name:     "an enumeration is not split between its items",
			sentence: "If you've already got the AirPods 4, 3, or 2, well, here's how they all compare.",
			want: []string{
				"If you've already got the AirPods 4, 3, or 2, well,",
				"here's how they all compare.",
			},
		},
		{
			// "The AirPods 3," was a cue of its own, 0.80s long.
			name:     "an appositive does not end a clause",
			sentence: "The AirPods 3, however, were about a 6 out of 10.",
			want:     []string{"The AirPods 3, however, were about a 6 out of 10."},
		},
		{
			// The rule the comma split exists for, and it still holds.
			name:     "a comma between two full clauses still splits",
			sentence: "and it's also a bit more snug to help with passive noise cancellation, which in turn helps with active noise cancellation.",
			want: []string{
				"and it's also a bit more snug to help with passive noise cancellation,",
				"which in turn helps with active noise cancellation.",
			},
		},
		{
			// The case the "first, not last" rule was written for.
			name:     "a full stop still ends a clause",
			sentence: "Hello there, my friend. How are you",
			want:     []string{"Hello there, my friend.", "How are you"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// One word at a time, which is how the rolling captions arrive.
			words := strings.Fields(c.sentence)
			pieces := make([]piece, len(words))
			for i, w := range words {
				pieces[i] = piece{start: float64(i), end: float64(i + 1), text: w}
			}

			cues := groupIntoClauses(pieces)
			got := make([]string, len(cues))
			for i, cue := range cues {
				got[i] = cue.Text
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("groupIntoClauses(%q) =\n  %q\nwant\n  %q", c.sentence, got, c.want)
			}
		})
	}
}

// A blind cut must not beat a full stop to it. This run has no punctuation
// until its last two words; at 30 it came out as "...maybe slightly" plus
// "below those." — a cue 0.72s long that says nothing on its own.
func TestForceSplitLetsANearbySentenceFinish(t *testing.T) {
	sentence := "I would say that they're even better than the original AirPods Pros " +
		"were and maybe even on the same level as the AirPods Pro 2os or maybe " +
		"okay maybe slightly below those."

	words := strings.Fields(sentence)
	pieces := make([]piece, len(words))
	for i, w := range words {
		pieces[i] = piece{start: float64(i), end: float64(i + 1), text: w}
	}

	cues := groupIntoClauses(pieces)
	if len(cues) != 1 {
		t.Fatalf("got %d cues, want 1: %+v", len(cues), cues)
	}
	if !strings.HasSuffix(cues[0].Text, "below those.") {
		t.Errorf("clause does not reach its full stop: %q", cues[0].Text)
	}
}
