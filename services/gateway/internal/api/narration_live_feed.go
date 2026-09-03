package api

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

var errLivePlaylist = errors.New("caption playlist refused")

// How far back from the live edge a pass begins.
//
// Two segments, about ten seconds: enough that the first clause has a
// predecessor to be joined to, and short enough that nothing is spoken about a
// picture already gone.
const liveEdgeLookback = 2

// liveSegment is one entry of the caption playlist, with the wall clock it
// begins at.
type liveSegment struct {
	sequence int
	at       time.Time
	url      string
}

// parseLivePlaylist reads the segments out of a rolling caption playlist.
//
// The clock comes from two tags and one arithmetic step, all of it measured
// against a real broadcast:
//
//	segment(sq) = EXT-X-PROGRAM-DATE-TIME + (sq - EXT-X-MEDIA-SEQUENCE) x EXTINF
//
// `EXT-X-PROGRAM-DATE-TIME` belongs to the *first* segment listed, and each
// following one is its own `EXTINF` later. Consecutive segments were confirmed
// to be exactly five seconds apart by an independent route — their
// `X-TIMESTAMP-MAP` values differ by 450000 ticks of a 90 kHz clock — so the
// two descriptions agree.
//
// A playlist with no date carries no answer this can use, and returns nothing
// rather than inventing a zero.
func parseLivePlaylist(raw string) []liveSegment {
	var (
		out      []liveSegment
		sequence = -1
		at       time.Time
		duration = 5.0
		next     = duration
		haveDate bool
	)

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue

		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			n, err := strconv.Atoi(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"))
			if err == nil {
				sequence = n
			}

		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			t, err := time.Parse(time.RFC3339, strings.TrimPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"))
			if err == nil {
				at = t
				haveDate = true
			}

		case strings.HasPrefix(line, "#EXTINF:"):
			value := strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ",")
			if d, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && d > 0 {
				next = d
			}

		case strings.HasPrefix(line, "#"):
			continue

		default:
			if sequence < 0 || !haveDate {
				// Without both, a segment has no place on the clock, and a clip
				// with the wrong time is worse than one that never played.
				return nil
			}
			out = append(out, liveSegment{sequence: sequence, at: at, url: line})
			sequence++
			at = at.Add(time.Duration(next * float64(time.Second)))
			next = duration
		}
	}
	return out
}

// liveCaptionFeed turns a stream of caption segments into finished clauses.
//
// It holds the two things that cannot be worked out from one segment alone:
// which segments have already been read, and the words of a clause that is not
// finished yet.
type liveCaptionFeed struct {
	lastSequence int

	// started is false until the first playlist has been seen.
	//
	// A caption playlist is not a thirty-second window. Measured on a real
	// broadcast it listed **2880 segments** — four hours of DVR — so a feed
	// that begins by reading everything it is offered begins four hours behind
	// the picture, and then spends the LLM and the synthesiser catching up on
	// speech nobody is listening to. The first playlist is therefore used to
	// find the live edge and nothing else.
	//
	// This is the same lesson as a recorded pass starting where the viewer is
	// rather than at zero, arriving from the other direction.
	started bool

	// builder gathers word groups into clauses on punctuation, exactly as the
	// recorded path does. Live captions arrive as fragments — "shows the
	// aftermath of" is a whole cue — and translating those one at a time is
	// what machine translation handles worst.
	builder clauseBuilder

	// lastText is the previous cue's words, for joining a cue that a segment
	// boundary cut in half.
	lastText string

	// recent is the last few finished clauses, sent to the model as context.
	recent []string
}

// liveClause is one finished clause and the moment it began.
type liveClause struct {
	at      time.Time
	text    string
	context []string
}

// absorb reads one segment and returns whatever clauses it completed.
//
// Cue times inside a segment are relative to that segment, so the wall clock of
// a cue is the segment's plus its own offset — which is the whole of the
// timing, and the reason nothing here touches a presentation timestamp.
func (f *liveCaptionFeed) absorb(segmentAt time.Time, body string) []liveClause {
	for _, block := range readBlocks(body) {
		if len(block.lines) == 0 {
			continue
		}
		text := cleanCueText(strings.Join(block.lines, " "))
		if text == "" {
			continue
		}
		// A cue that spans a segment boundary is published in both, split in
		// two with the same words. Measured on a live broadcast: "shows the
		// aftermath of" ends one segment at 3→5s and opens the next at 0→1s.
		// Taking both would say it twice.
		if text == f.lastText {
			continue
		}
		f.lastText = text

		at := segmentAt.Add(time.Duration(block.start * float64(time.Second)))
		f.builder.buf = append(f.builder.buf, piece{
			// Seconds since the epoch, so the clause builder's arithmetic —
			// written for offsets inside a file — works unchanged on a clock.
			start: float64(at.UnixNano()) / float64(time.Second),
			end:   float64(at.Add(time.Duration((block.end-block.start)*float64(time.Second))).UnixNano()) / float64(time.Second),
			text:  text,
		})

		for {
			cut := firstClauseBoundary(f.builder.joined())
			if cut < 0 {
				break
			}
			f.builder.emit(cut)
			if len(f.builder.buf) == 0 {
				break
			}
		}

		// A clause that never reaches punctuation still has to be said. The
		// recorded path splits on a word count for the same reason.
		if len(strings.Fields(f.builder.joined())) >= forceSplitWords {
			f.builder.emit(len([]rune(f.builder.joined())))
		}
	}

	var out []liveClause
	for _, cue := range f.builder.out {
		clause := liveClause{
			at:      time.Unix(0, int64(cue.Start*float64(time.Second))),
			text:    cue.Text,
			context: append([]string(nil), f.recent...),
		}
		out = append(out, clause)

		f.recent = append(f.recent, cue.Text)
		if len(f.recent) > narrationContext {
			f.recent = f.recent[len(f.recent)-narrationContext:]
		}
	}
	// The emitted list is not needed again, and a broadcast runs for days.
	f.builder.out = nil
	return out
}
