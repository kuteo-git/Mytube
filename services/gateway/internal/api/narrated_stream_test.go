package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The narrated stream for a Google speaker: made as it is fetched, so the
// voice comes seconds after narration is switched on — "ko nghe thuyết minh"
// when the whole pass had to finish first.

func TestARangeIsAPlaceInTheStream(t *testing.T) {
	cases := []struct {
		header      string
		first, last int64
		partial, ok bool
	}{
		{"", 0, 999, false, true},
		{"bytes=0-", 0, 999, true, true},
		{"bytes=44-143", 44, 143, true, true},
		{"bytes=900-5000", 900, 999, true, true},
		{"bytes=-100", 900, 999, true, true},
		{"bytes=1000-", 0, 0, false, false},
		{"bytes=5-2", 0, 0, false, false},
		{"bytes=0-1,5-9", 0, 0, false, false},
		{"items=0-1", 0, 0, false, false},
	}
	for _, c := range cases {
		first, last, partial, ok := byteRange(c.header, 1000)
		if ok != c.ok || (ok && (first != c.first || last != c.last || partial != c.partial)) {
			t.Errorf("%q: %d-%d partial=%v ok=%v", c.header, first, last, partial, ok)
		}
	}
}

func TestTheHeaderSaysHowLongTheSongIs(t *testing.T) {
	frames := int64(narratedRate * 3)
	wav := append(wavHeader(frames), make([]byte, frames*wavFrameBytes)...)
	if secs, ok := wavDuration(wav); !ok || math.Abs(secs-3) > 1e-9 {
		t.Fatalf("duration = %v %v", secs, ok)
	}
	if _, ok := wavPCM16(wav); !ok {
		t.Fatal("not read back as 16-bit PCM at the stream's rate")
	}
}

func TestEveryCueInTheBlockMustBeSpoken(t *testing.T) {
	cues := func(spoken ...bool) []passCue {
		out := make([]passCue, len(spoken))
		for i, s := range spoken {
			out[i] = passCue{start: float64(i + 1), spoken: s}
		}
		return out
	}
	if !spokenIn(cues(true, false, false), 0.9, 2) {
		t.Error("only the cue at 1s starts in 0.9–2s")
	}
	if spokenIn(cues(true, false, true), 1.9, 2.5) {
		t.Error("the cue at 2s is not spoken yet")
	}
	// A pass working from 450s leaves the start for last: a stream from 450s
	// must not wait on it (measured: 0.1s of sound in 40s).
	if !spokenIn(cues(false, false, true), 2.5, 3.1) {
		t.Error("waited on lines before the block")
	}
}

// streamFixture is a 4s tone on disk, a pass that knows one line at 1.5s
// which it has not spoken yet, and the line's clip: half a second of silence.
func streamFixture(t *testing.T) (*Gateway, string, string) {
	t.Helper()
	src := toneFixture(t)
	g := narratedGateway(t)
	dir := filepath.Join(g.mediaRoot, "v", narrationTTSDir)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "line.wav"), wav(narratedRate, 1, make([]int16, narratedRate/2)), 0o644)
	g.narration.passes["v"] = &narrationPass{
		Status: narrationRunning, Total: 1, gen: 1,
		cues: []passCue{{start: 1.5}},
	}
	return g, src, "/media/v/" + narrationTTSDir + "/line.wav"
}

func speakLater(g *Gateway, clipURL string, after time.Duration) {
	go func() {
		time.Sleep(after)
		g.addNarrationClip("v", 1, narrationClip{StartSeconds: 1.5, DurationSeconds: 1, ClipURL: clipURL})
		g.cueDone("v", 1, 0)
	}()
}

func pcmRMS(pcm []byte, from, to float64) float64 {
	var sum float64
	n := 0
	for i := int(from * narratedRate); i < int(to*narratedRate) && (i+1)*wavFrameBytes <= len(pcm); i++ {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i*wavFrameBytes:]))) / 32768
		sum += v * v
		n++
	}
	return math.Sqrt(sum / float64(max(n, 1)))
}

func TestTheStreamWaitsForALineAndThenDucksUnderIt(t *testing.T) {
	g, src, clip := streamFixture(t)
	frames := int64(4 * narratedRate)
	total := wavHeaderBytes + frames*wavFrameBytes
	speakLater(g, clip, 400*time.Millisecond)

	var out bytes.Buffer
	started := time.Now()
	if err := g.streamNarrated(context.Background(), &out, "v", src, frames, 0, total-1); err != nil {
		t.Fatal(err)
	}
	if int64(out.Len()) != total {
		t.Fatalf("%d bytes, the header promised %d", out.Len(), total)
	}
	if took := time.Since(started); took < 400*time.Millisecond {
		t.Fatalf("done in %s: the block with the line was sent before the pass had spoken it", took)
	}
	pcm := out.Bytes()[wavHeaderBytes:]
	before, during := pcmRMS(pcm, 0.5, 1.4), pcmRMS(pcm, 1.7, 2.4)
	if ratio := during / before; math.Abs(ratio-narratedDuckLevel) > 0.03 {
		t.Fatalf("the song under the line is %.2f of itself, want %.2f", ratio, narratedDuckLevel)
	}
}

func TestASeekIsTheSameSongFromThere(t *testing.T) {
	g, src, clip := streamFixture(t)
	speakLater(g, clip, 0)
	frames := int64(4 * narratedRate)
	total := wavHeaderBytes + frames*wavFrameBytes
	from := int64(wavHeaderBytes + 2*narratedRate*wavFrameBytes + 2) // 2s in, mid-frame

	var out bytes.Buffer
	if err := g.streamNarrated(context.Background(), &out, "v", src, frames, from, total-1); err != nil {
		t.Fatal(err)
	}
	if int64(out.Len()) != total-from {
		t.Fatalf("%d bytes for a range of %d", out.Len(), total-from)
	}
	// Realigned to the frame it started inside: the song from 2s, still under
	// the line until 2.5s, then back up.
	pcm := out.Bytes()[wavFrameBytes-2:]
	under, after := pcmRMS(pcm, 0.1, 0.4), pcmRMS(pcm, 0.7, 1.4)
	if ratio := under / after; math.Abs(ratio-narratedDuckLevel) > 0.05 {
		t.Fatalf("2.1–2.4s is %.2f of the song after the line, want %.2f", ratio, narratedDuckLevel)
	}
}

func TestAStoppedPassDoesNotHoldTheSongForEver(t *testing.T) {
	g, src, _ := streamFixture(t)
	frames := int64(4 * narratedRate)
	// The pass failed: nothing will be spoken, and the song goes on.
	g.narration.passes["v"].Status = narrationFailed
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- g.streamNarrated(context.Background(), &out, "v", src, frames, 0, wavHeaderBytes+frames*wavFrameBytes-1)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a failed pass held the stream")
	}
}
