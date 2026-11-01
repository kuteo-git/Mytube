package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
)

// A song's narrated sound: its own sound with the narration mixed in, for a
// speaker. A speaker plays one address it is handed and cannot play a second
// voice over it the way a phone does, so the two are made into one file —
// served with ranges like audio.m4a, which is what lets the speaker seek it,
// report its length and, on the R1, download it whole.
//
// Made only once the video's narration pass has finished. Until then the cast
// service plays the song as it is and asks again (`POST`, below) every second;
// a pass already on disk finishes in about no time, a new one in a few
// minutes (measured: 507 lines in 461s).
//
//	POST /api/videos/{id}/audio/narrated?from=<s>   start what is missing; answers at once
//	GET  /api/videos/{id}/audio/narrated   the file, or 404 not_ready

// The levels the phone's narrator uses by default (the app's
// DEFAULT_VOICE_LEVEL and DEFAULT_DUCK_LEVEL): the voice at half of master,
// the song at a fifth of it while a line has its slot.
const (
	narratedVoiceLevel = 0.5
	narratedDuckLevel  = 0.2
	// How long the song takes to go down for a line and come back. The phone
	// switches at once; in one file a step that sharp is a click.
	narratedRampSeconds = 0.03

	narratedRate     = 48000
	narratedChannels = 2

	// How long a mix that failed is answered as failed before it is tried
	// again: the cast service asks every second.
	narratedRetryAfter = 2 * time.Minute
)

type narratedAnswer struct {
	// The file: ready, preparing, none (nothing to narrate) or failed.
	Status string `json:"status"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
	// The stream (narrated.wav): ready once a line is spoken — the stream
	// waits on the rest itself, block by block — and none when there is
	// nothing to narrate. See streamAnswer.
	Stream string `json:"stream"`
}

type narratedMixes struct {
	mu      sync.Mutex
	running map[string]bool
	failed  map[string]time.Time
	// The song's length, by video, for the stream's header.
	durations map[string]float64
}

func newNarratedMixes() *narratedMixes {
	return &narratedMixes{running: map[string]bool{}, failed: map[string]time.Time{}, durations: map[string]float64{}}
}

func (g *Gateway) handlePrepareNarratedAudio(w http.ResponseWriter, r *http.Request) {
	v, ok := g.narratableVideo(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, g.prepareNarrated(r.Context(), v.GetId(), narrationStartAt(r), g.narratedSource(r.Context(), v)))
}

// narratableVideo reads the video a narrated-sound request names, or
// answers the request itself: a bad id, the catalogue's error, or 422 for a
// broadcast, which has no end to mix to.
func (g *Gateway) narratableVideo(w http.ResponseWriter, r *http.Request) (*catalogv1.Video, bool) {
	videoID := r.PathValue("id")
	if _, err := safeVideoDir(g.mediaRoot, videoID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad video id"})
		return nil, false
	}
	resp, err := g.catalog.GetVideo(r.Context(), connect.NewRequest(&catalogv1.GetVideoRequest{VideoId: videoID}))
	if err != nil {
		g.writeErr(w, r, err)
		return nil, false
	}
	v := resp.Msg.GetVideo()
	if decideLive(v.GetLiveStatus(), v.GetIsLiveNow()) == liveYes || v.GetLiveStatus() == "is_upcoming" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "live"})
		return nil, false
	}
	return v, true
}

// narratedSource is where the song's own sound is read from: its audio.m4a
// when the video is on disk, ingest's stream of YouTube's track otherwise.
// An empty answer means the cut failed, and the mix waits for the next ask.
func (g *Gateway) narratedSource(ctx context.Context, v *catalogv1.Video) func() (string, error) {
	return func() (string, error) {
		if v.GetMediaState() == catalogv1.MediaState_MEDIA_STATE_READY && v.GetMediaPath() != "" &&
			checkMedia(g.mediaRoot, v.GetMediaPath()) == mediaPresent {
			return g.cutAudio(ctx, v.GetId(), filepath.Join(g.mediaRoot, v.GetMediaPath()))
		}
		return g.ingestBaseURL + "/audio/" + url.PathEscape(v.GetId()), nil
	}
}

// streamAnswer is whether a pass has a narrated stream to offer: ready once
// a line has been spoken, none when it ended with nothing said. Not on the
// pass merely running — a song with no captions, or none translated, would be
// handed to the speaker as narrated and the remote would say so, for good,
// over a song nothing is spoken in. Read under g.narration.mu.
func streamAnswer(pass *narrationPass) string {
	switch {
	case pass == nil:
		return "preparing"
	case len(pass.Clips) > 0:
		return "ready"
	case pass.Status == narrationRunning || pass.Status == narrationIdle:
		return "preparing"
	}
	return "none"
}

// prepareNarrated answers where the song's narrated sound is, and starts the
// step that is missing: the narration pass, from where the speaker is
// (`from`), or the mix.
func (g *Gateway) prepareNarrated(ctx context.Context, videoID string, from float64, source func() (string, error)) narratedAnswer {
	g.narration.mu.Lock()
	pass := g.narration.passes[videoID]
	var status narrationStatus
	var done, total int
	var clips []narrationClip
	stream := streamAnswer(pass)
	if pass != nil {
		status, done, total = pass.Status, pass.Done, pass.Total
		clips = append(clips, pass.Clips...)
	}
	g.narration.mu.Unlock()

	switch {
	case pass == nil || status == narrationIdle:
		// Not running, or cancelled part way (the phone's close button). A
		// pass over lines already on disk is a read, so this is cheap when
		// the phone has narrated it before.
		g.startNarrationPass(context.WithoutCancel(ctx), videoID, from)
		// A moment for its first line, so the stream can be offered on this
		// answer rather than one reload later. A second at most: the cast
		// service asks inside its tick.
		stream = "preparing"
		for wait := 0; wait < 10 && stream == "preparing" && sleepCtx(ctx, 100*time.Millisecond); wait++ {
			g.narration.mu.Lock()
			stream = streamAnswer(g.narration.passes[videoID])
			g.narration.mu.Unlock()
		}
		return narratedAnswer{Status: "preparing", Stream: stream}
	case status == narrationRunning:
		return narratedAnswer{Status: "preparing", Done: done, Total: total, Stream: stream}
	case status == narrationFailed:
		return narratedAnswer{Status: "failed", Done: done, Total: total, Stream: "none"}
	case len(clips) == 0:
		return narratedAnswer{Status: "none", Done: done, Total: total, Stream: "none"}
	}

	dir, _ := safeVideoDir(g.mediaRoot, videoID)
	key := narratedKey(clips)
	// Catalog's eviction removes these with the media file, by the same
	// pattern (usecase.NarratedMixPattern).
	dst := filepath.Join(dir, "narrated-"+key+".m4a")
	if _, err := os.Stat(dst); err == nil {
		return narratedAnswer{Status: "ready", Done: done, Total: total, Stream: "ready"}
	}

	g.narratedMixes.mu.Lock()
	defer g.narratedMixes.mu.Unlock()
	if at, ok := g.narratedMixes.failed[key]; ok && time.Since(at) < narratedRetryAfter {
		return narratedAnswer{Status: "failed", Done: done, Total: total, Stream: "ready"}
	}
	if !g.narratedMixes.running[key] {
		g.narratedMixes.running[key] = true
		go g.mixNarrated(videoID, key, dir, dst, clips, source)
	}
	return narratedAnswer{Status: "preparing", Done: done, Total: total, Stream: "ready"}
}

func (g *Gateway) mixNarrated(videoID, key, dir, dst string, clips []narrationClip, source func() (string, error)) {
	started := time.Now()
	err := func() error {
		src, err := source()
		if err != nil {
			return err
		}
		lines := g.linesOf(clips)
		// Into a file of its own first, as the audio cut is: a mix that dies
		// half way never leaves a file that looks whole.
		tmp := fmt.Sprintf("%s.%d.part", dst, time.Now().UnixNano())
		defer func() { _ = os.Remove(tmp) }()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		if err := mixNarration(ctx, src, lines, tmp); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
		// The mix before this one, made from an earlier set of lines.
		old, _ := filepath.Glob(filepath.Join(dir, "narrated-*.m4a"))
		for _, f := range old {
			if f != dst {
				_ = os.Remove(f)
			}
		}
		return nil
	}()

	g.narratedMixes.mu.Lock()
	delete(g.narratedMixes.running, key)
	if err != nil {
		g.narratedMixes.failed[key] = time.Now()
	} else {
		delete(g.narratedMixes.failed, key)
	}
	g.narratedMixes.mu.Unlock()

	if err != nil {
		g.logger.Error("narrated sound", "video", videoID, "error", err)
		return
	}
	g.logger.Info("narrated sound mixed", "video", videoID, "lines", len(clips),
		"took", time.Since(started).Round(time.Millisecond))
}

// narratedKey names a mix by what it was made from, so a pass that changed
// its lines (another voice, a retranslation) makes a new file.
func narratedKey(clips []narrationClip) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "v1 %g %g\n", narratedVoiceLevel, narratedDuckLevel)
	for _, c := range clips {
		_, _ = fmt.Fprintf(h, "%.3f %.3f %s\n", c.StartSeconds, c.DurationSeconds, c.ClipURL)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (g *Gateway) handleNarratedAudio(w http.ResponseWriter, r *http.Request) {
	dir, err := safeVideoDir(g.mediaRoot, r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad video id"})
		return
	}
	// The newest: an older one has just been replaced and is removed.
	found, _ := filepath.Glob(filepath.Join(dir, "narrated-*.m4a"))
	var newest string
	var newestAt time.Time
	for _, f := range found {
		if info, err := os.Stat(f); err == nil && info.ModTime().After(newestAt) {
			newest, newestAt = f, info.ModTime()
		}
	}
	if newest == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_ready"})
		return
	}
	f, err := os.Open(newest)
	if err != nil {
		http.Error(w, "cannot read audio", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "audio/mp4")
	http.ServeContent(w, r, "narrated.m4a", newestAt, f)
}

// ---- the mix ----

// narratedLine is one spoken line: its clip, the moment it belongs at and
// the slot the song is turned down for. Pcm is read when the line is reached.
type narratedLine struct {
	At, Slot float64
	Path     string
	Pcm      []float32 // mono, narratedRate
}

func (l narratedLine) end() float64 {
	return l.At + math.Max(l.Slot, float64(len(l.Pcm))/narratedRate)
}

// mixNarration reads `src` (a file or an address ffmpeg can open), mixes the
// lines over it and writes an AAC file to `dst`.
func mixNarration(ctx context.Context, src string, lines []narratedLine, dst string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pcm := fmt.Sprint(narratedRate)
	ch := fmt.Sprint(narratedChannels)
	dec := decodePCM(ctx, src, 0, narratedChannels)
	enc := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y",
		"-f", "s16le", "-ac", ch, "-ar", pcm, "-i", "pipe:0",
		"-c:a", "aac", "-b:a", "160k", "-movflags", "+faststart", "-f", "mp4", dst)
	var decErr, encErr strings.Builder
	dec.Stderr, enc.Stderr = &decErr, &encErr
	in, err := dec.StdoutPipe()
	if err != nil {
		return err
	}
	out, err := enc.StdinPipe()
	if err != nil {
		return err
	}
	if err := dec.Start(); err != nil {
		return err
	}
	if err := enc.Start(); err != nil {
		_ = dec.Process.Kill()
		_ = dec.Wait()
		return err
	}

	m := newNarrationMixer(lines, readClip)
	copyErr := m.run(bufio.NewReaderSize(in, 1<<16), out)
	_ = out.Close()
	if copyErr != nil {
		cancel()
	}
	decWait, encWait := dec.Wait(), enc.Wait()
	switch {
	case copyErr != nil:
		return copyErr
	case decWait != nil:
		return fmt.Errorf("ffmpeg decode: %w: %s", decWait, tail([]byte(decErr.String()), 512))
	case encWait != nil:
		return fmt.Errorf("ffmpeg encode: %w: %s", encWait, tail([]byte(encErr.String()), 512))
	}
	return nil
}

// narrationMixer turns the song down under each line's slot and adds the
// line, frame by frame, as the song streams through.
type narrationMixer struct {
	lines  []narratedLine
	read   func(path string) ([]float32, error)
	next   int
	active []narratedLine
	gain   float64
	frame  int64
	// The latest line taken in, so lines handed in again are not spoken twice.
	admittedAt float64
}

func newNarrationMixer(lines []narratedLine, read func(string) ([]float32, error)) *narrationMixer {
	m := &narrationMixer{read: read, gain: 1, admittedAt: math.Inf(-1)}
	m.refresh(lines)
	return m
}

// refresh hands the mixer the lines known now, for a stream whose lines are
// still arriving. Those already taken in keep sounding; the rest are picked
// up as the song reaches them.
func (m *narrationMixer) refresh(lines []narratedLine) {
	sorted := append([]narratedLine(nil), lines...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At < sorted[j].At })
	m.lines = sorted
	m.next = sort.Search(len(sorted), func(i int) bool { return sorted[i].At > m.admittedAt })
}

// run mixes interleaved s16le stereo from r to w until r ends.
func (m *narrationMixer) run(r io.Reader, w io.Writer) error {
	const frames = narratedRate / 10
	buf := make([]byte, frames*narratedChannels*2)
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			n -= n % (narratedChannels * 2)
			m.mix(buf[:n])
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// mix works on a block of interleaved s16le stereo in place.
func (m *narrationMixer) mix(block []byte) {
	step := 1 / (narratedRampSeconds * narratedRate)
	for i := 0; i+narratedChannels*2 <= len(block); i += narratedChannels * 2 {
		t := float64(m.frame) / narratedRate
		m.admit(t)

		target, voice := 1.0, 0.0
		for _, l := range m.active {
			if t < l.At+l.Slot {
				target = narratedDuckLevel
			}
			if k := int((t - l.At) * narratedRate); k >= 0 && k < len(l.Pcm) {
				voice += float64(l.Pcm[k]) * narratedVoiceLevel
			}
		}
		switch {
		case m.gain > target:
			m.gain = math.Max(target, m.gain-step)
		case m.gain < target:
			m.gain = math.Min(target, m.gain+step)
		}

		for c := 0; c < narratedChannels; c++ {
			at := i + c*2
			song := float64(int16(binary.LittleEndian.Uint16(block[at:]))) / 32768
			v := math.Max(-1, math.Min(1, song*m.gain+voice))
			binary.LittleEndian.PutUint16(block[at:], uint16(int16(math.Round(v*32767))))
		}
		m.frame++
	}
}

// admit brings in the lines that have begun by t and lets go of those over.
// A clip that cannot be read costs its line, not the song; its slot still
// turns the song down, as on the phone, where the line is only silent.
func (m *narrationMixer) admit(t float64) {
	kept := m.active[:0]
	for _, l := range m.active {
		if t < l.end() {
			kept = append(kept, l)
		}
	}
	m.active = kept
	for m.next < len(m.lines) && m.lines[m.next].At <= t {
		l := m.lines[m.next]
		m.next++
		m.admittedAt = l.At
		// Over before the song got here: a stream begun past it.
		if l.At+l.Slot <= t-narratedRampSeconds {
			continue
		}
		if l.Pcm == nil && l.Path != "" {
			l.Pcm, _ = m.read(l.Path)
		}
		m.active = append(m.active, l)
	}
}

// decodePCM is ffmpeg reading `src` from `from` seconds as s16le at
// narratedRate, to its stdout.
func decodePCM(ctx context.Context, src string, from float64, channels int) *exec.Cmd {
	args := []string{"-v", "error"}
	if from > 0 {
		args = append(args, "-ss", strconv.FormatFloat(from, 'f', 3, 64))
	}
	args = append(args, "-i", src, "-vn", "-f", "s16le", "-ac", fmt.Sprint(channels), "-ar", fmt.Sprint(narratedRate), "pipe:1")
	return exec.CommandContext(ctx, "ffmpeg", args...)
}

// readClip reads a line's WAV as mono at narratedRate. The synthesiser's
// clips are 16-bit PCM at that rate already (ffprobed 40 of 40); anything
// else is handed to ffmpeg.
func readClip(path string) ([]float32, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if pcm, ok := wavPCM16(raw); ok {
		return pcm, nil
	}
	out, err := decodePCM(context.Background(), path, 0, 1).Output()
	if err != nil {
		return nil, err
	}
	pcm := make([]float32, len(out)/2)
	for i := range pcm {
		pcm[i] = float32(int16(binary.LittleEndian.Uint16(out[i*2:]))) / 32768
	}
	return pcm, nil
}

// wavPCM16 reads a WAV that is 16-bit PCM at narratedRate, folded to mono.
// False for anything else.
func wavPCM16(wav []byte) ([]float32, bool) {
	format, data, ok := wavChunks(wav)
	if !ok || len(format) < 16 || binary.LittleEndian.Uint16(format) != 1 {
		return nil, false
	}
	channels := int(binary.LittleEndian.Uint16(format[2:]))
	rate := binary.LittleEndian.Uint32(format[4:])
	bits := binary.LittleEndian.Uint16(format[14:])
	if channels < 1 || bits != 16 || rate != narratedRate {
		return nil, false
	}
	frames := len(data) / (2 * channels)
	pcm := make([]float32, frames)
	for f := 0; f < frames; f++ {
		var sum float32
		for c := 0; c < channels; c++ {
			sum += float32(int16(binary.LittleEndian.Uint16(data[(f*channels+c)*2:])))
		}
		pcm[f] = sum / float32(channels) / 32768
	}
	return pcm, true
}
