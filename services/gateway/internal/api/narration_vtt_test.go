package api

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"regexp"
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

			cues := groupIntoClauses(pieces, false)
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

	cues := groupIntoClauses(pieces, false)
	if len(cues) != 1 {
		t.Fatalf("got %d cues, want 1: %+v", len(cues), cues)
	}
	if !strings.HasSuffix(cues[0].Text, "below those.") {
		t.Errorf("clause does not reach its full stop: %q", cues[0].Text)
	}
}

// Automatic captions with no punctuation at all.
//
// testdata/narration-bare-asr.en.vtt is the first minute of t_YyrMV7SuE, copied
// verbatim: old ASR, lowercase, not one of . , ? ! in 3,577 words. With nothing
// to cut on, the 40-word ceiling was the only boundary, so 89 of its 90 cues
// were exactly 40 words and "every new idea that comes out of there they
// adopted immediately" was voiced as two halves. The expected cues below were
// worked out from this file's word timestamps (a pause is >= 0.7s between the
// starts of two words, with five words already gathered), not from the parser.

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// vttBetween keeps the blocks from the one whose timing line contains from, up
// to but not including the one whose timing line contains to.
func vttBetween(raw, from, to string) string {
	var kept []string
	on := false
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(line, "-->") {
			if strings.Contains(line, from) {
				on = true
			}
			if strings.Contains(line, to) {
				on = false
			}
		}
		if on {
			kept = append(kept, line)
		}
	}
	return "WEBVTT\n\n" + strings.Join(kept, "\n") + "\n"
}

// shiftVTT moves every timestamp later by whole seconds.
func shiftVTT(raw string, seconds int) string {
	return timestampRe.ReplaceAllStringFunc(raw, func(ts string) string {
		ms := int(parseVTTTime(ts)*1000+0.5) + seconds*1000
		return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
	})
}

var timestampRe = regexp.MustCompile(`\d{2}:\d{2}:\d{2}\.\d{3}`)

type cueAt struct {
	start float64
	text  string
}

func startsAndTexts(cues []vttCue) []cueAt {
	out := make([]cueAt, len(cues))
	for i, c := range cues {
		out[i] = cueAt{c.Start, c.Text}
	}
	return out
}

func assertCues(t *testing.T, got []vttCue, want []cueAt) {
	t.Helper()
	if len(got) < len(want) {
		t.Fatalf("got %d cues, want at least %d: %+v", len(got), len(want), startsAndTexts(got))
	}
	for i, w := range want {
		if math.Abs(got[i].Start-w.start) > 1e-6 || got[i].Text != w.text {
			t.Errorf("cue %d = {%v %q}, want {%v %q}", i, got[i].Start, got[i].Text, w.start, w.text)
		}
	}
}

func TestBareCaptionsEndAClauseAtAPauseInSpeech(t *testing.T) {
	cues := parseVTT(readTestdata(t, "narration-bare-asr.en.vtt"), "en")

	assertCues(t, cues, []cueAt{
		{0.16, "some of you know what i'm talking about can you turn a compliment into an argument that's what i want to know"},
		{8.08, "i i live in los angeles california"},
		{10.08, "and uh every new idea that comes out of there they adopted immediately"},
		{13.679, "and i'm a little more conservative"},
		{15.36, "like the new one is any man that says they're a woman immediately you must address him as a woman and i'm progressive i'm like all right but they should have to pass a test first"},
		{24.0, "because women have skills men don't have you can't just say i'm a woman do something a woman can do then i'll believe you"},
		{30.0, "right can you break into my iphone and the time i go to the bathroom and come back to the dinner table"},
		{38.239, "just something basic can you be totally starving but have no idea what you want to"},
	})

	// A clause ends where the last word of it ends: "california" runs until "and".
	if math.Abs(cues[1].End-10.08) > 1e-6 {
		t.Errorf("second cue ends at %v, want 10.08", cues[1].End)
	}

	for _, c := range cues {
		if n := len(strings.Fields(c.Text)); n >= forceSplitWords {
			t.Errorf("cue at %v is %d words, a blind cut where speech paused: %q", c.Start, n, c.Text)
		}
	}
}

func TestBareCaptionsPauseAcrossApplauseStartsAtTheNextWord(t *testing.T) {
	// "[Applause]" runs from 6.29 to 8.07 and "i" is spoken at 8.08. The time the
	// applause took is a pause, and the line after it begins with its own word.
	cues := parseVTT(readTestdata(t, "narration-bare-asr.en.vtt"), "en")
	if len(cues) < 2 {
		t.Fatalf("got %d cues", len(cues))
	}
	if !strings.HasSuffix(cues[0].Text, "that's what i want to know") {
		t.Errorf("first cue = %q, want it to end where the speech did", cues[0].Text)
	}
	if strings.Contains(cues[0].Text, "applause") || strings.Contains(cues[1].Text, "applause") {
		t.Errorf("applause was voiced: %q / %q", cues[0].Text, cues[1].Text)
	}
	if math.Abs(cues[1].Start-8.08) > 1e-6 {
		t.Errorf("second cue starts at %v, want 8.08 (not the applause's 6.29)", cues[1].Start)
	}
}

// The same twenty words, 8.08s to 13.67s of the fixture, once bare and once with
// a single full stop on the last word. A punctuated track has long comma-less
// stretches that are not pauses between clauses, so the 0.8s between
// "california" and "and" ends a clause only in the bare track.
func TestPauseEndsAClauseOnlyWhereTheTrackHasNoPunctuation(t *testing.T) {
	bare := vttBetween(readTestdata(t, "narration-bare-asr.en.vtt"), "00:00:08.080", "00:00:13.679")
	punctuated := strings.Replace(bare, "immediately</c>", "immediately.</c>", 1)
	if punctuated == bare {
		t.Fatal("fixture slice has no 'immediately' to punctuate")
	}

	assertCues(t, parseVTT(bare, "en"), []cueAt{
		{8.08, "i i live in los angeles california"},
		{10.08, "and uh every new idea that comes out of there they adopted immediately"},
	})

	got := parseVTT(punctuated, "en")
	want := []cueAt{{8.08, "i i live in los angeles california and uh every new idea that comes out of there they adopted immediately."}}
	if !reflect.DeepEqual(startsAndTexts(got), want) {
		t.Errorf("punctuated track = %+v, want one cue %+v", startsAndTexts(got), want)
	}
}

func TestBareCaptionsWithNoPauseStillCutAtTheCeiling(t *testing.T) {
	// Words from the fixture, said at an even 0.25s: no pause anywhere.
	words := strings.Fields("like the new one is any man that says they're a woman immediately " +
		"you must address him as a woman and i'm progressive i'm like all right but " +
		"they should have to pass a test first because women have skills men don't " +
		"have you can't")
	if len(words) != 45 {
		t.Fatalf("test has %d words, want 45", len(words))
	}
	var line strings.Builder
	line.WriteString(words[0])
	for i, w := range words[1:] {
		fmt.Fprintf(&line, "<00:00:%06.3f><c> %s</c>", float64(i+1)*0.25, w)
	}
	raw := "WEBVTT\n\n00:00:00.000 --> 00:00:11.500\n \n" + line.String() + "\n"

	cues := parseVTT(raw, "en")
	if len(cues) != 2 {
		t.Fatalf("got %d cues, want 2: %+v", len(cues), startsAndTexts(cues))
	}
	if cues[0].Text != strings.Join(words[:40], " ") || cues[1].Text != strings.Join(words[40:], " ") {
		t.Errorf("cut is not at word 40: %q / %q", cues[0].Text, cues[1].Text)
	}
}

func TestPauseNeedsFiveWordsBeforeIt(t *testing.T) {
	// "and uh every new idea" with the pause after the fourth word, then after
	// the fifth. Times are the fixture's, 10.08 onward, with one gap widened.
	cases := []struct {
		name string
		line string
		want []string
	}{
		{
			name: "four words then a pause",
			line: "and<00:00:10.320><c> uh</c><00:00:10.719><c> every</c><00:00:11.120><c> new</c><00:00:12.000><c> idea</c><00:00:12.240><c> that</c>",
			want: []string{"and uh every new idea that"},
		},
		{
			name: "five words then a pause",
			line: "and<00:00:10.320><c> uh</c><00:00:10.719><c> every</c><00:00:11.120><c> new</c><00:00:11.360><c> idea</c><00:00:12.240><c> that</c>",
			want: []string{"and uh every new idea", "that"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw := "WEBVTT\n\n00:00:10.080 --> 00:00:13.000\n \n" + c.line + "\n"
			var got []string
			for _, cue := range parseVTT(raw, "en") {
				got = append(got, cue.Text)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// One stray full stop in 338 words does not make a track punctuated: three
// bare tracks in the library carry one. The threshold is a rate (fewer than one
// mark per hundred words), not "none at all".
func TestOneStrayMarkDoesNotMakeATrackPunctuated(t *testing.T) {
	first := readTestdata(t, "narration-bare-asr.en.vtt")
	first = strings.Replace(first, "<c> table</c>", "<c> table.</c>", 1)
	raw := first + "\n" + shiftVTT(readTestdata(t, "narration-bare-asr.en.vtt"), 60)

	cues := parseVTT(raw, "en")
	assertCues(t, cues[:7], []cueAt{
		{0.16, "some of you know what i'm talking about can you turn a compliment into an argument that's what i want to know"},
		{8.08, "i i live in los angeles california"},
		{10.08, "and uh every new idea that comes out of there they adopted immediately"},
		{13.679, "and i'm a little more conservative"},
		{15.36, "like the new one is any man that says they're a woman immediately you must address him as a woman and i'm progressive i'm like all right but they should have to pass a test first"},
		{24.0, "because women have skills men don't have you can't just say i'm a woman do something a woman can do then i'll believe you"},
		{30.0, "right can you break into my iphone and the time i go to the bathroom and come back to the dinner table."},
	})

	// The second copy is cut at its pauses as well.
	byStart := map[int64]string{}
	for _, c := range cues {
		byStart[int64(math.Round(c.Start*1000))] = c.Text
	}
	for start, text := range map[int64]string{
		68080: "i i live in los angeles california",
		70080: "and uh every new idea that comes out of there they adopted immediately",
		73679: "and i'm a little more conservative",
	} {
		if byStart[start] != text {
			t.Errorf("cue at %.3f = %q, want %q", float64(start)/1000, byStart[start], text)
		}
	}
}

func TestVietnameseBareCaptionsSplitOnPausesToo(t *testing.T) {
	// _f5QLW5tphE: Vietnamese ASR with no punctuation, 39 words in the first
	// 13s. Below the 40-word ceiling, so before this it was one cue.
	cues := parseVTT(readTestdata(t, "narration-bare-asr.vi.vtt"), "vi")
	assertCues(t, cues, []cueAt{
		{0.32, "chỉ có lời hứa thôi Mà lỡ dở ra tôi còn buồn như vậy con langan còn buồn như vậy Huân"},
		{7.6, "chi hoàn cảnh của cha con ông phủ Trần Còn khổ sở đến mức nào nữa"},
		{12.44, "chị Chị"},
	})
	if len(cues) != 3 {
		t.Errorf("got %d cues, want 3", len(cues))
	}
}

func TestOtherLanguagesAreStillNotSplit(t *testing.T) {
	cues := parseVTT(readTestdata(t, "narration-bare-asr.en.vtt"), "ja")
	if len(cues) != 169 {
		t.Fatalf("got %d cues, want one per word (169)", len(cues))
	}
	if cues[0].Text != "some" || math.Abs(cues[0].Start-0.16) > 1e-6 {
		t.Errorf("first cue = %+v, want the word 'some' at 0.16", cues[0])
	}
}

func TestLacksPunctuationIsARateNotAnAbsence(t *testing.T) {
	words := func(n int, marked string) []piece {
		ps := make([]piece, n)
		for i := range ps {
			ps[i] = piece{text: "word"}
		}
		if marked != "" {
			ps[0].text = marked
		}
		return ps
	}
	cases := []struct {
		name   string
		pieces []piece
		want   bool
	}{
		{"no marks", words(300, ""), true},
		{"one mark in 300 words", words(300, "end."), true},
		{"one mark in 101 words", words(101, "end."), true},
		{"exactly one per hundred", words(100, "end."), false},
		{"commas count", words(50, "a,"), false},
		{"question and exclamation count", append(words(150, "why?"), piece{text: "no!"}), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := lacksPunctuation(c.pieces); got != c.want {
				t.Errorf("lacksPunctuation = %v, want %v", got, c.want)
			}
		})
	}
}

// A line with no word timing (a rolling cue without tags, which is how an
// automatic file's last lines can arrive) is one piece holding several words.
// Its start is the line's start, so measured start to start the pause before the
// next line would be the whole line's duration.
func TestPauseAfterAMultiWordPieceIsMeasuredFromItsEnd(t *testing.T) {
	line := "one two three four five six"
	cases := []struct {
		name      string
		nextStart float64
		want      int
	}{
		{"half a second after the line ends", 2.5, 1},
		{"a second after the line ends", 3.0, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cues := groupIntoClauses([]piece{
				{start: 0, end: 2, text: line},
				{start: c.nextStart, end: c.nextStart + 1, text: "seven"},
			}, true)
			if len(cues) != c.want {
				t.Errorf("got %d cues, want %d: %+v", len(cues), c.want, cues)
			}
		})
	}
}

func TestPauseBeforeIsMeasuredStartToStartAfterASingleWord(t *testing.T) {
	// A word's end is set to the next word's start, so measuring from the end
	// would see no pause at all.
	last := piece{start: 4.24, end: 8.08, text: "know"}
	next := piece{start: 8.08, text: "i"}
	if got := pauseBefore(last, next); math.Abs(got-3.84) > 1e-9 {
		t.Errorf("pauseBefore = %v, want 3.84", got)
	}
}
