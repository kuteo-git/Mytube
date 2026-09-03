package api

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func TestTempoForRoomySlotDoesNotDrawl(t *testing.T) {
	// Ten seconds of room for a two-second line. Nothing to hurry for — but a
	// wide slot is not a reason to read slower than natural either.
	got, err := tempoFor(2.0, 10.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != defaultSpeed {
		t.Fatalf("want %v, got %v", defaultSpeed, got)
	}
}

func TestTempoForTightSlotSpeedsUp(t *testing.T) {
	// 3s of speech into a 2s slot needs 1.5x.
	got, err := tempoFor(3.0, 2.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if math.Abs(got-1.5) > 1e-9 {
		t.Fatalf("want 1.5, got %v", got)
	}
}

// The rule that replaced clamping. A line needing more than maxSpeed used to be
// squeezed to 3.0 and played anyway: unintelligible, and it overran its slot,
// which pushed the next clip past its own cue and lost that one as well. Two
// lines spent to keep one nobody could follow.
func TestTempoForRefusesRatherThanClamping(t *testing.T) {
	_, err := tempoFor(10.0, 2.0) // needs 5x
	if !errors.Is(err, errTooFast) {
		t.Fatalf("want errTooFast, got %v", err)
	}
}

func TestTempoForExactlyAtCeilingIsAllowed(t *testing.T) {
	got, err := tempoFor(6.0, 2.0) // exactly 3.0
	if err != nil {
		t.Fatalf("the ceiling itself must be playable: %v", err)
	}
	if math.Abs(got-maxSpeed) > 1e-9 {
		t.Fatalf("want %v, got %v", maxSpeed, got)
	}
}

// The last cue of a video has no following cue, so the slot can arrive as zero.
// Dividing by it would produce +Inf and then a nonsense atempo chain.
func TestTempoForZeroInputsFallBackInsteadOfDividing(t *testing.T) {
	for _, c := range []struct{ natural, slot float64 }{
		{0, 2}, {2, 0}, {0, 0}, {-1, 2}, {2, -1},
	} {
		got, err := tempoFor(c.natural, c.slot)
		if err != nil {
			t.Fatalf("natural=%v slot=%v: %v", c.natural, c.slot, err)
		}
		if got != defaultSpeed {
			t.Fatalf("natural=%v slot=%v: want %v, got %v",
				c.natural, c.slot, defaultSpeed, got)
		}
	}
}

// ---- wavDuration ------------------------------------------------------------

// buildWAV assembles a WAV out of the chunks given, so a test can put them in
// an awkward order or insert ones the parser should walk past.
func buildWAV(t *testing.T, chunks ...[]byte) []byte {
	t.Helper()
	var body bytes.Buffer
	body.WriteString("WAVE")
	for _, c := range chunks {
		body.Write(c)
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

func fmtChunk(byteRate uint32) []byte {
	var c bytes.Buffer
	c.WriteString("fmt ")
	_ = binary.Write(&c, binary.LittleEndian, uint32(16))
	_ = binary.Write(&c, binary.LittleEndian, uint16(1))     // PCM
	_ = binary.Write(&c, binary.LittleEndian, uint16(1))     // mono
	_ = binary.Write(&c, binary.LittleEndian, uint32(24000)) // sample rate
	_ = binary.Write(&c, binary.LittleEndian, byteRate)      // byte rate
	_ = binary.Write(&c, binary.LittleEndian, uint16(2))     // block align
	_ = binary.Write(&c, binary.LittleEndian, uint16(16))    // bits
	return c.Bytes()
}

func dataChunk(payload int) []byte {
	var c bytes.Buffer
	c.WriteString("data")
	_ = binary.Write(&c, binary.LittleEndian, uint32(payload))
	c.Write(make([]byte, payload))
	return c.Bytes()
}

func TestWavDurationCanonicalFile(t *testing.T) {
	// 48000 bytes/s, 96000 bytes of audio -> 2 seconds.
	wav := buildWAV(t, fmtChunk(48000), dataChunk(96000))
	got, ok := wavDuration(wav)
	if !ok {
		t.Fatal("a canonical WAV must be readable")
	}
	if math.Abs(got-2.0) > 1e-9 {
		t.Fatalf("want 2s, got %v", got)
	}
}

// The reason chunks are walked rather than read at offset 44. A synthesiser is
// free to emit LIST or fact chunks first, and a parser assuming the canonical
// layout would measure those bytes as audio.
func TestWavDurationWalksPastUnknownChunks(t *testing.T) {
	list := append([]byte("LIST"), []byte{10, 0, 0, 0}...)
	list = append(list, make([]byte, 10)...)

	wav := buildWAV(t, fmtChunk(48000), list, dataChunk(48000))
	got, ok := wavDuration(wav)
	if !ok {
		t.Fatal("unknown chunks must be skipped, not fatal")
	}
	if math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("want 1s, got %v", got)
	}
}

// An odd-sized chunk is followed by a pad byte. Without honouring it every
// later chunk header is read one byte out of alignment.
func TestWavDurationHonoursWordAlignmentPadding(t *testing.T) {
	odd := append([]byte("junk"), []byte{3, 0, 0, 0}...)
	odd = append(odd, 1, 2, 3, 0) // 3 bytes of body + 1 pad

	wav := buildWAV(t, fmtChunk(48000), odd, dataChunk(24000))
	got, ok := wavDuration(wav)
	if !ok {
		t.Fatal("padding must be honoured")
	}
	if math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("want 0.5s, got %v", got)
	}
}

// A clip cut short in transit declares more data than it carries. Measuring it
// as its intended length would stretch it by the wrong factor — and the
// stretched result gets cached, so the error would outlive the bad transfer.
func TestWavDurationTrustsActualLengthOverDeclaredSize(t *testing.T) {
	full := buildWAV(t, fmtChunk(48000), dataChunk(96000))
	truncated := full[:len(full)-48000] // half the audio lost

	got, ok := wavDuration(truncated)
	if !ok {
		t.Fatal("a truncated file is still measurable")
	}
	if math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("want the 1s actually present, got %v", got)
	}
}

func TestWavDurationRejectsNonRIFF(t *testing.T) {
	// What an upstream error page looks like when it arrives where audio was
	// expected.
	if _, ok := wavDuration([]byte("<html>502 Bad Gateway</html>")); ok {
		t.Fatal("non-RIFF bytes must not be reported as audio")
	}
	if _, ok := wavDuration(nil); ok {
		t.Fatal("empty input must not be reported as audio")
	}
	if _, ok := wavDuration([]byte("RIFF")); ok {
		t.Fatal("a truncated header must not be reported as audio")
	}
}

func TestWavDurationDataBeforeFmtIsRefusedNotGuessed(t *testing.T) {
	wav := buildWAV(t, dataChunk(48000), fmtChunk(48000))
	if _, ok := wavDuration(wav); ok {
		t.Fatal("with no byte rate yet there is nothing to divide by")
	}
}

func TestWavDurationMissingDataChunk(t *testing.T) {
	wav := buildWAV(t, fmtChunk(48000))
	if _, ok := wavDuration(wav); ok {
		t.Fatal("a WAV with no data chunk has no duration")
	}
}

// cueIndexAt decides where a pass begins, and getting it wrong is a viewer
// hearing the wrong line first — so the edges are held here rather than
// discovered on a phone.
func TestCueIndexAtStartsInsideTheLineBeingSpoken(t *testing.T) {
	cues := []vttCue{
		{Start: 0, End: 2, Text: "one"},
		{Start: 2, End: 4, Text: "two"},
		{Start: 4, End: 6, Text: "three"},
	}

	// Sitting inside "two" wants "two", not the line after it.
	if got := cueIndexAt(cues, 3); got != 1 {
		t.Fatalf("inside the second cue: got %d, want 1", got)
	}
	// Exactly on a boundary is the cue that begins there.
	if got := cueIndexAt(cues, 4); got != 2 {
		t.Fatalf("on the third cue's start: got %d, want 2", got)
	}
	if got := cueIndexAt(cues, 0); got != 0 {
		t.Fatalf("at the beginning: got %d, want 0", got)
	}
}

func TestCueIndexAtPastTheEndAsksForTheWholeVideo(t *testing.T) {
	cues := []vttCue{{Start: 0, End: 2, Text: "one"}}
	// A stale position must not mean "narrate nothing".
	if got := cueIndexAt(cues, 900); got != 0 {
		t.Fatalf("past the end: got %d, want 0", got)
	}
}

func TestCueIndexAtOnAnEmptyListIsZero(t *testing.T) {
	if got := cueIndexAt(nil, 12); got != 0 {
		t.Fatalf("no cues: got %d, want 0", got)
	}
}
