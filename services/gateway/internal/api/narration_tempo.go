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

	// defaultSpeed matches DEFAULT_SPEED: VieNeu-TTS reads slightly slow.
	defaultSpeed = 1.1

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
// by byte rate, both of which are in chunks near the front of the file. Chunks
// are walked rather than assumed to be at fixed offsets — the synthesiser is
// free to emit LIST or fact chunks between `fmt ` and `data`, and a reader that
// assumed the canonical 44-byte layout would measure those as audio.
func wavDuration(wav []byte) (float64, bool) {
	// "RIFF" + size + "WAVE" is 12 bytes before the first chunk header.
	if len(wav) < 12 || string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return 0, false
	}

	var byteRate uint32
	pos := 12
	for pos+8 <= len(wav) {
		id := string(wav[pos : pos+4])
		size := binary.LittleEndian.Uint32(wav[pos+4 : pos+8])
		body := pos + 8

		switch id {
		case "fmt ":
			// byteRate sits at offset 8 within the chunk and already folds in
			// sample rate, channel count and bit depth — so nothing else has to
			// be read to turn bytes into seconds.
			if size < 16 || body+16 > len(wav) {
				return 0, false
			}
			byteRate = binary.LittleEndian.Uint32(wav[body+8 : body+12])
		case "data":
			if byteRate == 0 {
				// `data` before `fmt ` is legal in the letter of the format and
				// unheard of in practice; there is nothing to divide by.
				return 0, false
			}
			// Trust the file's own length over the declared size: a clip cut
			// short in transit would otherwise measure as its intended length
			// and be stretched by the wrong factor.
			n := int(size)
			if avail := len(wav) - body; n > avail || n == 0 {
				n = avail
			}
			return float64(n) / float64(byteRate), true
		}

		// Chunks are word-aligned: an odd size is followed by a pad byte.
		pos = body + int(size) + int(size&1)
	}
	return 0, false
}
