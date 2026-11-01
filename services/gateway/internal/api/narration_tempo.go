package api

import (
	"encoding/binary"
	"errors"
	"math"
)

// The tempo rules, mirrored from narration-schedule.ts.
//
// They live here as well as in the browser because the decision moved: the
// client knows how much time a line has (the gap to the next cue, which is in
// the subtitle file), but only the synthesiser knows how long the line actually
// takes to say. Asking the client to find that out cost a second round trip and
// a second synthesis at a different tempo — and because the cache key includes
// tempo, the second one was always a miss. Long lines were therefore the slowest
// to arrive and the likeliest to be dropped for arriving late.
//
// Giving the server the slot lets it answer in one trip: synthesise once at
// natural speed, measure, stretch to fit.
const (
	// naturalSpeed is the tempo a clip comes out of the synthesiser at, and the
	// key the unstretched copy is cached under. Stretching is pure ffmpeg, so
	// one natural copy serves every tempo the same words are ever asked for.
	naturalSpeed = 1.0

	// defaultSpeed matches DEFAULT_SPEED: the floor a line is read at when its
	// slot has room to spare. It is the synthesiser's own tempo, so a line is
	// only ever hurried by the time it actually has — never by a standing
	// opinion that the voice is slow.
	//
	// It being equal to naturalSpeed is load-bearing rather than a coincidence:
	// the two then share a cache key, so the common case runs no atempo and
	// writes no second file, and every natural copy already on disk answers a
	// default-tempo request outright. The day this moves off 1.0 again they part,
	// and TestTTSNaturalAndDefaultShareAKeyWhileTheyAreEqual says so.
	defaultSpeed = 1.0

	// maxSpeed matches MAX_SPEED. Past this a line stops being followable
	// whatever the timing says, so it is dropped rather than played — see
	// errTooFast.
	maxSpeed = 3.0
)

// errTooFast reports a line that cannot be said in the time it has, even at
// maxSpeed.
//
// The old behaviour was to clamp to maxSpeed and play it anyway, which spent
// two lines to keep one: the clip was gibberish at that tempo, and it overran
// its slot, which pushed the following clip past its own cue and lost that one
// too. Refusing it costs one line and leaves the next intact.
var errTooFast = errors.New("line does not fit even at maximum tempo")

// tempoFor works out how fast to read a line so it fits the time available.
//
// Never below defaultSpeed — a slot with room to spare is not a reason to
// drawl — and errTooFast rather than a clamp above maxSpeed.
func tempoFor(naturalDuration, slot float64) (float64, error) {
	if !(naturalDuration > 0) || !(slot > 0) {
		return defaultSpeed, nil
	}
	needed := naturalDuration / slot
	if needed > maxSpeed {
		return 0, errTooFast
	}
	return math.Max(defaultSpeed, needed), nil
}

// wavDuration reads how many seconds of audio a WAV holds.
//
// Parsed from the header rather than decoded: the answer is data bytes divided
// by byte rate, both of which are in chunks near the front of the file.
func wavDuration(wav []byte) (float64, bool) {
	format, data, ok := wavChunks(wav)
	// byteRate sits at offset 8 within the chunk and already folds in sample
	// rate, channel count and bit depth — so nothing else has to be read to
	// turn bytes into seconds.
	if !ok || len(format) < 16 {
		return 0, false
	}
	byteRate := binary.LittleEndian.Uint32(format[8:12])
	if byteRate == 0 {
		return 0, false
	}
	return float64(len(data)) / float64(byteRate), true
}

// wavChunks finds a WAV's `fmt ` and `data` chunk bodies.
//
// Chunks are walked rather than assumed to be at fixed offsets — the
// synthesiser is free to emit LIST or fact chunks between `fmt ` and `data`,
// and a reader that assumed the canonical 44-byte layout would measure those
// as audio. False when `data` comes before `fmt `: legal in the letter of the
// format, unheard of in practice, and nothing could be read from it.
func wavChunks(wav []byte) (format, data []byte, ok bool) {
	// "RIFF" + size + "WAVE" is 12 bytes before the first chunk header.
	if len(wav) < 12 || string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, nil, false
	}
	pos := 12
	for pos+8 <= len(wav) {
		id := string(wav[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(wav[pos+4 : pos+8]))
		body := pos + 8

		switch id {
		case "fmt ":
			if size < 16 || body+16 > len(wav) {
				return nil, nil, false
			}
			format = wav[body : body+16]
		case "data":
			if format == nil {
				return nil, nil, false
			}
			// Trust the file's own length over the declared size: a clip cut
			// short in transit would otherwise measure as its intended length
			// and be stretched by the wrong factor.
			n := size
			if avail := len(wav) - body; n > avail || n == 0 {
				n = avail
			}
			return format, wav[body : body+n], true
		}

		// Chunks are word-aligned: an odd size is followed by a pad byte.
		pos = body + size + size&1
	}
	return nil, nil, false
}
