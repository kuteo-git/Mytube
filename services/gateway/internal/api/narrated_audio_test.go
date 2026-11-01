package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A song's narrated sound, for a speaker: the song turned down under each
// line and the line over it — "bật thuyết minh thì ko có work khi cast".

// stereo builds interleaved s16le stereo of `seconds` at a constant level.
func stereo(seconds, level float64) []byte {
	frames := int(seconds * narratedRate)
	out := make([]byte, frames*4)
	v := uint16(int16(math.Round(level * 32767)))
	for i := 0; i < frames; i++ {
		binary.LittleEndian.PutUint16(out[i*4:], v)
		binary.LittleEndian.PutUint16(out[i*4+2:], v)
	}
	return out
}

func levelAt(pcm []byte, seconds float64) float64 {
	i := int(seconds*narratedRate) * 4
	return float64(int16(binary.LittleEndian.Uint16(pcm[i:]))) / 32767
}

func constant(seconds float64, level float32) []float32 {
	out := make([]float32, int(seconds*narratedRate))
	for i := range out {
		out[i] = level
	}
	return out
}

func TestTheSongIsTurnedDownUnderALineAndTheLineIsOverIt(t *testing.T) {
	song := stereo(2, 0.5)
	m := newNarrationMixer([]narratedLine{{At: 0.5, Slot: 0.6, Pcm: constant(0.4, 0.2)}}, nil)
	var out bytes.Buffer
	if err := m.run(bytes.NewReader(song), &out); err != nil {
		t.Fatal(err)
	}
	got := out.Bytes()
	if len(got) != len(song) {
		t.Fatalf("%d bytes out for %d in", len(got), len(song))
	}
	cases := []struct {
		at   float64
		want float64
		why  string
	}{
		{0.3, 0.5, "before the line, the song as it is"},
		{0.7, 0.5*narratedDuckLevel + 0.2*narratedVoiceLevel, "the song ducked, the line over it"},
		{1.0, 0.5 * narratedDuckLevel, "the line over, its slot still holds the song down"},
		{1.5, 0.5, "after the slot, the song back up"},
	}
	for _, c := range cases {
		if v := levelAt(got, c.at); math.Abs(v-c.want) > 0.002 {
			t.Errorf("at %.1fs = %.4f, want %.4f: %s", c.at, v, c.want, c.why)
		}
	}
	// Ramped, not stepped: half way down the ramp is between the two levels.
	if v := levelAt(got, 0.5+narratedRampSeconds/2); v > 0.45 || v < 0.15 {
		t.Errorf("at the ramp's middle = %.3f: the song stepped down, which is a click", v)
	}
}

func TestALineThatCannotBeReadCostsOnlyThatLine(t *testing.T) {
	m := newNarrationMixer([]narratedLine{{At: 0.2, Slot: 0.3, Path: "missing.wav"}},
		func(string) ([]float32, error) { return nil, os.ErrNotExist })
	var out bytes.Buffer
	if err := m.run(bytes.NewReader(stereo(1, 0.5)), &out); err != nil {
		t.Fatal(err)
	}
	if v := levelAt(out.Bytes(), 0.8); math.Abs(v-0.5) > 0.002 {
		t.Fatalf("after the line = %.3f, want the song", v)
	}
}

func wav(rate, channels int, samples []int16) []byte {
	var b bytes.Buffer
	data := len(samples) * 2
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate*channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data))
	_ = binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}

func TestTheSynthesisersWavIsReadAsMono(t *testing.T) {
	pcm, ok := wavPCM16(wav(narratedRate, 2, []int16{16384, 0, -16384, -16384}))
	if !ok || len(pcm) != 2 || math.Abs(float64(pcm[0])-0.25) > 1e-6 || math.Abs(float64(pcm[1])+0.5) > 1e-6 {
		t.Fatalf("pcm = %v ok = %v", pcm, ok)
	}
	if _, ok := wavPCM16(wav(24000, 1, []int16{1, 2})); ok {
		t.Fatal("another rate was read as if it were 48 kHz")
	}
}

func narratedGateway(t *testing.T) *Gateway {
	t.Helper()
	return &Gateway{
		mediaRoot: t.TempDir(), configDir: t.TempDir(),
		narration: newNarrationRegistry(), narratedMixes: newNarratedMixes(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestPrepareAnswersWhereTheNarratedSoundIs(t *testing.T) {
	g := narratedGateway(t)
	noSource := func() (string, error) { return "", os.ErrNotExist }
	clip := narrationClip{StartSeconds: 1, DurationSeconds: 2, ClipURL: "/media/v/narration-tts/a.wav"}

	g.narration.passes["v"] = &narrationPass{Status: narrationRunning, Done: 3, Total: 10}
	if a := g.prepareNarrated(context.Background(), "v", 0, noSource); a.Status != "preparing" || a.Done != 3 || a.Total != 10 {
		t.Errorf("running pass: %+v", a)
	}
	g.narration.passes["v"] = &narrationPass{Status: narrationFailed}
	if a := g.prepareNarrated(context.Background(), "v", 0, noSource); a.Status != "failed" {
		t.Errorf("failed pass: %+v", a)
	}
	g.narration.passes["v"] = &narrationPass{Status: narrationDone}
	if a := g.prepareNarrated(context.Background(), "v", 0, noSource); a.Status != "none" {
		t.Errorf("a video with nothing to narrate: %+v", a)
	}

	clips := []narrationClip{clip}
	g.narration.passes["v"] = &narrationPass{Status: narrationDone, Clips: clips}
	dir := filepath.Join(g.mediaRoot, "v")
	_ = os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "narrated-"+narratedKey(clips)+".m4a"), []byte("m4a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if a := g.prepareNarrated(context.Background(), "v", 0, noSource); a.Status != "ready" {
		t.Errorf("mixed already: %+v", a)
	}
}

func TestAMixThatFailedIsNotRetriedEverySecond(t *testing.T) {
	g := narratedGateway(t)
	clips := []narrationClip{{StartSeconds: 1, DurationSeconds: 2, ClipURL: "/media/v/narration-tts/a.wav"}}
	g.narration.passes["v"] = &narrationPass{Status: narrationDone, Clips: clips}
	calls := 0
	source := func() (string, error) { calls++; return "", os.ErrNotExist }

	if a := g.prepareNarrated(context.Background(), "v", 0, source); a.Status != "preparing" {
		t.Fatalf("first ask: %+v", a)
	}
	waitFor(t, func() bool {
		g.narratedMixes.mu.Lock()
		defer g.narratedMixes.mu.Unlock()
		return len(g.narratedMixes.failed) == 1
	}, "the mix never failed")
	if a := g.prepareNarrated(context.Background(), "v", 0, source); a.Status != "failed" {
		t.Fatalf("asked again: %+v", a)
	}
	if calls != 1 {
		t.Fatalf("mixed %d times", calls)
	}
}

func TestTheNarratedSoundIsServedWithRanges(t *testing.T) {
	g := narratedGateway(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/videos/v/audio/narrated", nil)
	req.SetPathValue("id", "v")
	g.handleNarratedAudio(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("nothing mixed: %d", rec.Code)
	}

	dir := filepath.Join(g.mediaRoot, "v")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "narrated-abc.m4a"), []byte("0123456789"), 0o644)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/videos/v/audio/narrated", nil)
	req.SetPathValue("id", "v")
	req.Header.Set("Range", "bytes=2-4")
	g.handleNarratedAudio(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "234" || rec.Header().Get("Content-Type") != "audio/mp4" {
		t.Fatalf("status %d body %q type %q", rec.Code, rec.Body, rec.Header().Get("Content-Type"))
	}
}

// Through the real ffmpeg, both ways: a tone in, a line of silence over it,
// and the file out is AAC, as long as the song, and quieter in the line's slot.
func TestFFmpegMixesTheNarrationIntoTheSong(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "audio.m4a")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=f=440:d=4:sample_rate=48000",
		"-c:a", "aac", src).CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v %s", err, out)
	}
	clip := filepath.Join(dir, "line.wav")
	_ = os.WriteFile(clip, wav(narratedRate, 1, make([]int16, narratedRate/2)), 0o644)
	dst := filepath.Join(dir, "narrated.m4a")

	if err := mixNarration(context.Background(), src, []narratedLine{{At: 1.5, Slot: 1, Path: clip}}, dst); err != nil {
		t.Fatal(err)
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name:format=duration",
		"-of", "default=nw=1", dst).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(probe, []byte("codec_name=aac")) {
		t.Fatalf("probe = %s", probe)
	}
	pcm, err := exec.Command("ffmpeg", "-v", "error", "-i", dst, "-f", "s16le", "-ac", "2", "-ar", "48000", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	if secs := float64(len(pcm)) / 4 / narratedRate; math.Abs(secs-4) > 0.1 {
		t.Fatalf("%.2fs out of a 4s song", secs)
	}
	rms := func(from, to float64) float64 {
		var sum float64
		n := 0
		for i := int(from * narratedRate); i < int(to*narratedRate); i++ {
			v := float64(int16(binary.LittleEndian.Uint16(pcm[i*4:]))) / 32768
			sum += v * v
			n++
		}
		return math.Sqrt(sum / float64(n))
	}
	before, during := rms(0.5, 1.4), rms(1.7, 2.4)
	if ratio := during / before; math.Abs(ratio-narratedDuckLevel) > 0.03 {
		t.Fatalf("the song in the slot is %.2f of itself, want %.2f", ratio, narratedDuckLevel)
	}
}
