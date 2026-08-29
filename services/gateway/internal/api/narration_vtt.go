package api

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// WebVTT -> narration cues.
//
// A port of web/src/features/watch/application/narration-vtt.ts, moved here so
// the narration pass can run on the server. The reasoning in that file holds
// unchanged and is not repeated; what follows records only what is different in
// Go, and the one thing the port had to get right that TypeScript got for free.
//
// **Offsets are runes, not bytes.** The original counts in UTF-16 code units and
// slices the joined text by those offsets. Counting bytes here would put every
// cut in the wrong place the moment a line contains a Vietnamese vowel — and the
// source of these captions is a library whose whole point is that it holds
// Vietnamese. Every offset below is an index into a []rune.
//
// Two caption shapes arrive from YouTube and they are different formats, not
// variations of one: manual cues carry complete sentences, automatic cues roll,
// each carrying the tail of the previous line plus one new phrase timestamped
// word by word.

// vttCue is one line of narration with the seconds it occupies.
type vttCue struct {
	Start float64 `json:"startSeconds"`
	End   float64 `json:"endSeconds"`
	Text  string  `json:"text"`
}

// snapshotMaxSeconds: cues shorter than this are YouTube's clean-snapshot
// copies of a line a real cue already carries, not speech.
const snapshotMaxSeconds = 0.1

// minClauseWords: a clause is not split off a comma if either side is shorter.
const minClauseWords = 3

// forceSplitWords: with no punctuation at all, a clause is cut at this length.
const forceSplitWords = 30

var (
	abbreviations = regexp.MustCompile(`(?i)^(Dr|Mr|Mrs|Ms|Prof|Sr|Jr|vs|etc)$`)
	tagRe         = regexp.MustCompile(`<[^>]+>`)
	doubleAngleRe = regexp.MustCompile(`>>\s*`)
	multiSpaceRe  = regexp.MustCompile(`\s{2,}`)
	wordTimingRe  = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}\.\d{3})><c>([^<]*)</c>`)
	leadTextRe    = regexp.MustCompile(`^([^<]+)<`)
	emotionRe     = regexp.MustCompile(`(?i)\[(cười|thở dài|hắng giọng)\]`)
	bracketRe     = regexp.MustCompile(`\s*\[[^\]]*\]\s*`)
	placeholderRe = regexp.MustCompile("\x00(\\d+)\x00")
)

// symbolsToDrop are read aloud by a synthesiser and mean nothing spoken.
const symbolsToDrop = "♪♫♬→←↑↓↔«»“”‘’„‚"

// parseVTTTime turns "00:01:23.456" into 83.456, or NaN.
func parseVTTTime(raw string) float64 {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 {
		return math.NaN()
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	s, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return math.NaN()
	}
	return float64(h)*3600 + float64(m)*60 + s
}

// cleanCueText strips WebVTT markup and the symbols a synthesiser would read
// out. Bracketed descriptions survive this step and are removed by
// stripBrackets, because a bracket can span several <c> tags and is not whole
// yet.
func cleanCueText(raw string) string {
	// Entities first, so &gt;&gt; is >> in time to be recognised below.
	s := strings.NewReplacer(
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
	).Replace(raw)

	s = tagRe.ReplaceAllString(s, "")
	s = doubleAngleRe.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(symbolsToDrop, r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	return multiSpaceRe.ReplaceAllString(s, " ")
}

// stripBrackets removes [sound effects] and keeps the emotion tags the TTS
// server understands.
func stripBrackets(text string) string {
	var placeholders []string
	s := emotionRe.ReplaceAllStringFunc(text, func(match string) string {
		placeholders = append(placeholders, match)
		return "\x00" + strconv.Itoa(len(placeholders)-1) + "\x00"
	})
	s = bracketRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, " .", ".")
	s = placeholderRe.ReplaceAllStringFunc(s, func(match string) string {
		i, err := strconv.Atoi(strings.Trim(match, "\x00"))
		if err != nil || i >= len(placeholders) {
			return ""
		}
		return placeholders[i]
	})
	s = multiSpaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// piece is one word or phrase with the time it is spoken.
type piece struct {
	start float64
	end   float64
	text  string
}

type rawBlock struct {
	start float64
	end   float64
	lines []string
}

// readBlocks splits the file into timed blocks.
//
// A block's payload ends at a blank line *or* at the next timing line — files in
// the wild have both. Only the blank separator may be stepped over; stepping
// over a timing line discards the cue that line introduced.
func readBlocks(raw string) []rawBlock {
	normalised := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalised, "\n")

	var blocks []rawBlock
	i := 0
	for i < len(lines) {
		if !strings.Contains(lines[i], "-->") {
			i++
			continue
		}

		timing := lines[i]
		arrow := strings.Index(timing, "-->")
		start := parseVTTTime(timing[:arrow])
		tail := strings.TrimSpace(timing[arrow+3:])
		if cut := strings.IndexAny(tail, " \t"); cut >= 0 {
			tail = tail[:cut]
		}
		end := parseVTTTime(tail)
		i++

		// Leading blank lines belong to the separator, not the payload.
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}

		var payload []string
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" &&
			!strings.Contains(lines[i], "-->") {
			payload = append(payload, lines[i])
			i++
		}

		if !math.IsNaN(start) && !math.IsNaN(end) {
			blocks = append(blocks, rawBlock{start: start, end: end, lines: payload})
		}
	}
	return blocks
}

// piecesFromTagged turns one automatic-caption block into word-level pieces.
//
// Only the last payload line is read: in the rolling format the earlier lines
// are the previous cue's text repeated for the viewer, and reading them is what
// makes a phrase say itself twice.
func piecesFromTagged(block rawBlock) []piece {
	line := ""
	if n := len(block.lines); n > 0 {
		line = block.lines[n-1]
	}

	var pieces []piece

	// Text before the first timestamp tag is spoken from the cue's own start.
	if lead := leadTextRe.FindStringSubmatch(line); lead != nil {
		if text := cleanCueText(lead[1]); text != "" {
			pieces = append(pieces, piece{start: block.start, text: text})
		}
	}

	for _, m := range wordTimingRe.FindAllStringSubmatch(line, -1) {
		at := parseVTTTime(m[1])
		text := cleanCueText(m[2])
		if text != "" && !math.IsNaN(at) {
			pieces = append(pieces, piece{start: at, text: text})
		}
	}

	if len(pieces) == 0 {
		if text := cleanCueText(line); text != "" {
			pieces = append(pieces, piece{start: block.start, end: block.end, text: text})
		}
		return pieces
	}

	// Each word runs until the next begins; the last runs to the cue's end.
	for k := range pieces {
		if k+1 < len(pieces) {
			pieces[k].end = pieces[k+1].start
		} else {
			pieces[k].end = block.end
		}
	}
	return pieces
}

// firstClauseBoundary reports the rune offset just past the first clause
// boundary in text, or -1.
//
// The *first*, not the last: taking the last swallows every boundary before it,
// so "Hello there, my friend. How are you" comes out as one cue instead of
// three.
func firstClauseBoundary(text string) int {
	runes := []rune(text)
	for idx, ch := range runes {
		if ch != '.' && ch != '!' && ch != '?' && ch != ',' {
			continue
		}
		var before, after rune
		if idx > 0 {
			before = runes[idx-1]
		}
		if idx+1 < len(runes) {
			after = runes[idx+1]
		}

		// "2.5", "3.14" — a decimal point ends nothing.
		if ch == '.' && isDigit(before) && isDigit(after) {
			continue
		}

		if ch == '.' && isLetter(before) && after == ' ' {
			fields := strings.Fields(string(runes[:idx]))
			if len(fields) > 0 && abbreviations.MatchString(fields[len(fields)-1]) {
				continue
			}
		}

		// Mid-word punctuation is not a boundary.
		if after != 0 && after != ' ' {
			continue
		}

		// A comma is a boundary only when both sides can stand alone. Splitting
		// "Then, how are you" leaves "Then," to be translated with no sentence
		// around it and voiced as a clip half a second long.
		if ch == ',' {
			wordsBefore := len(strings.Fields(string(runes[:idx])))
			wordsAfter := len(strings.Fields(string(runes[idx+1:])))
			if wordsBefore < minClauseWords || wordsAfter < minClauseWords {
				continue
			}
		}

		return idx + 1
	}
	return -1
}

func isDigit(r rune) bool  { return r >= '0' && r <= '9' }
func isLetter(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }

// clauseBuilder gathers word pieces into clauses.
//
// A clause carries the start of its first word and the end of its last, which is
// the point of keeping per-word timing this far: a clause claiming the timing of
// the block it came from would be wrong by however much of that block belonged
// to the clause before it.
type clauseBuilder struct {
	buf []piece
	out []vttCue
}

// emit closes a clause at a rune offset into the joined text of the buffer.
func (b *clauseBuilder) emit(upTo int) {
	consumed := 0
	cut := -1
	var leftover *piece

	for k := range b.buf {
		gap := 0
		if k > 0 {
			gap = 1 // the space join inserts
		}
		next := consumed + gap + len([]rune(b.buf[k].text))
		if upTo <= next {
			cut = k
			within := upTo - (consumed + gap)
			runes := []rune(b.buf[k].text)
			if within < 0 {
				within = 0
			}
			if within > len(runes) {
				within = len(runes)
			}
			if rest := strings.TrimSpace(string(runes[within:])); rest != "" {
				leftover = &piece{start: b.buf[k].start, end: b.buf[k].end, text: rest}
			}
			break
		}
		consumed = next
	}
	if cut < 0 {
		cut = len(b.buf) - 1
	}
	if cut < 0 {
		return
	}

	taken := b.buf[:cut+1]
	parts := make([]string, len(taken))
	for i, p := range taken {
		parts[i] = p.text
	}
	joined := []rune(strings.Join(parts, " "))
	if upTo < len(joined) {
		joined = joined[:upTo]
	}
	text := stripBrackets(strings.TrimSpace(string(joined)))
	if text != "" {
		b.out = append(b.out, vttCue{
			Start: taken[0].start,
			End:   taken[len(taken)-1].end,
			Text:  text,
		})
	}

	rest := append([]piece(nil), b.buf[cut+1:]...)
	if leftover != nil {
		b.buf = append([]piece{*leftover}, rest...)
	} else {
		b.buf = rest
	}
}

func (b *clauseBuilder) joined() string {
	parts := make([]string, len(b.buf))
	for i, p := range b.buf {
		parts[i] = p.text
	}
	return strings.Join(parts, " ")
}

func groupIntoClauses(pieces []piece) []vttCue {
	b := &clauseBuilder{}

	for _, p := range pieces {
		b.buf = append(b.buf, p)

		// One block can complete more than one clause.
		for {
			at := firstClauseBoundary(b.joined())
			if at < 0 {
				break
			}
			b.emit(at)
			if len(b.buf) == 0 {
				break
			}
		}

		joined := b.joined()
		if len(strings.Fields(joined)) >= forceSplitWords {
			b.emit(len([]rune(joined)))
		}
	}

	if len(b.buf) > 0 {
		text := stripBrackets(strings.TrimSpace(b.joined()))
		if text != "" {
			b.out = append(b.out, vttCue{
				Start: b.buf[0].start,
				End:   b.buf[len(b.buf)-1].end,
				Text:  text,
			})
		}
	}
	return b.out
}

// parseVTT reads a WebVTT file into the cues the narration speaks.
//
// sourceLang decides whether clause splitting applies: the rules are tuned for
// the punctuation of English and Vietnamese, and a language they do not describe
// is better left in whole cues than cut on rules that do not hold there.
func parseVTT(raw, sourceLang string) []vttCue {
	blocks := readBlocks(raw)

	isAuto := false
	for _, b := range blocks {
		for _, l := range b.lines {
			if strings.Contains(l, "<c>") {
				isAuto = true
				break
			}
		}
		if isAuto {
			break
		}
	}

	var pieces []piece
	var manual []vttCue

	for _, block := range blocks {
		if block.end-block.start < snapshotMaxSeconds {
			continue
		}
		if len(block.lines) == 0 {
			continue
		}

		last := block.lines[len(block.lines)-1]
		if strings.Contains(last, "<c>") {
			pieces = append(pieces, piecesFromTagged(block)...)
			continue
		}

		if isAuto {
			// Rolling text without tags: again only the last line is new.
			if text := cleanCueText(last); text != "" {
				pieces = append(pieces, piece{start: block.start, end: block.end, text: text})
			}
			continue
		}

		if text := cleanCueText(strings.Join(block.lines, " ")); text != "" {
			manual = append(manual, vttCue{Start: block.start, End: block.end, Text: text})
		}
	}

	if !isAuto {
		out := make([]vttCue, 0, len(manual))
		for _, c := range manual {
			if text := stripBrackets(c.Text); text != "" {
				out = append(out, vttCue{Start: c.Start, End: c.End, Text: text})
			}
		}
		return out
	}

	if sourceLang != "en" && sourceLang != "vi" {
		out := make([]vttCue, 0, len(pieces))
		for _, p := range pieces {
			if text := stripBrackets(p.text); text != "" {
				out = append(out, vttCue{Start: p.start, End: p.end, Text: text})
			}
		}
		return out
	}

	return groupIntoClauses(pieces)
}
