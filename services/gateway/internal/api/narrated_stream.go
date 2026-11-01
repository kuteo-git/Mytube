package api

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"

	catalogv1 "github.com/lucnguyen/local-youtube/gen/go/catalog/v1"
)

// The narrated sound as a stream, for a Google speaker: made as it is
// fetched, so the voice is there seconds after narration is switched on
// rather than when the whole pass and the mix are done — minutes for a video
// never narrated before (130 lines took past a minute and the listener gave
// up, 2026-11-01).
//
//	GET /api/videos/{id}/audio/narrated.wav
//
// WAV because its bytes are its time: 44 bytes of header, then four bytes a
// frame. So the stream has a length before a sample is made, and a range is a
// place in the song — the speaker seeks, shows the song's length and reports
// where it is, exactly as with a file. A block is mixed only once the pass has
// been through every line that starts in it; the pass runs about three times
// faster than the song plays, so after the first seconds that wait is rare.
// A seek far from where the pass is working moves the pass there.
//
// The R1 downloads a song whole before playing it, so it gets the file
// (narrated_audio.go), which needs the whole pass anyway.

const (
	wavHeaderBytes = 44
	wavFrameBytes  = narratedChannels * 2

	// How long one block waits for the pass before it is sent without the
	// lines it is missing: a pass that has stopped moving (the synthesiser
	// gone) must not hold the song.
	streamLineWait = 20 * time.Second
	// How long a block waits before the pass is asked to work from here.
	streamRetargetAfter = 3 * time.Second
)

func (g *Gateway) handleNarratedStream(w http.ResponseWriter, r *http.Request) {
	videoID := r.PathValue("id")
	if _, err := safeVideoDir(g.mediaRoot, videoID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad video id"})
		return
	}
	resp, err := g.catalog.GetVideo(r.Context(), connect.NewRequest(&catalogv1.GetVideoRequest{VideoId: videoID}))
	if err != nil {
		g.writeErr(w, r, err)
		return
	}
	v := resp.Msg.GetVideo()
	if decideLive(v.GetLiveStatus(), v.GetIsLiveNow()) == liveYes || v.GetLiveStatus() == "is_upcoming" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "live"})
		return
	}
	src, err := g.narratedSource(r.Context(), v)()
	if err != nil {
		g.logger.Error("narrated stream source", "video", videoID, "error", err)
		http.Error(w, "cannot prepare audio", http.StatusInternalServerError)
		return
	}
	seconds, err := g.audioDuration(r.Context(), videoID, src)
	if err != nil {
		g.logger.Error("narrated stream length", "video", videoID, "error", err)
		http.Error(w, "cannot read audio", http.StatusBadGateway)
		return
	}
	frames := int64(math.Round(seconds * narratedRate))
	total := wavHeaderBytes + frames*wavFrameBytes

	first, last, partial, ok := byteRange(r.Header.Get("Range"), total)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", total))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "audio/wav")
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Length", strconv.FormatInt(last-first+1, 10))
	if partial {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", first, last, total))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead {
		return
	}
	if err := g.streamNarrated(r.Context(), w, videoID, src, frames, first, last); err != nil &&
		!errors.Is(err, context.Canceled) {
		g.logger.Warn("narrated stream", "video", videoID, "from", first, "error", err)
	}
}

// byteRange reads a single "bytes=a-b" range against a body of `total`
// bytes. No header is the whole body; a range it cannot serve is !ok.
func byteRange(header string, total int64) (first, last int64, partial, ok bool) {
	if header == "" {
		return 0, total - 1, false, true
	}
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false, false
	}
	a, b, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false, false
	}
	var err error
	switch {
	case a == "":
		// The last b bytes.
		n, err := strconv.ParseInt(b, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, false
		}
		first, last = max(total-n, 0), total-1
	default:
		if first, err = strconv.ParseInt(a, 10, 64); err != nil || first < 0 || first >= total {
			return 0, 0, false, false
		}
		last = total - 1
		if b != "" {
			if last, err = strconv.ParseInt(b, 10, 64); err != nil || last < first {
				return 0, 0, false, false
			}
			last = min(last, total-1)
		}
	}
	return first, last, true, true
}

// wavHeader is the 44 bytes before `frames` frames of 16-bit stereo PCM.
func wavHeader(frames int64) []byte {
	data := uint32(frames * wavFrameBytes)
	h := make([]byte, wavHeaderBytes)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], 36+data)
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], narratedChannels)
	binary.LittleEndian.PutUint32(h[24:], narratedRate)
	binary.LittleEndian.PutUint32(h[28:], narratedRate*wavFrameBytes)
	binary.LittleEndian.PutUint16(h[32:], wavFrameBytes)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], data)
	return h
}

// streamNarrated writes bytes first..last of the narrated WAV.
func (g *Gateway) streamNarrated(ctx context.Context, w io.Writer, videoID, src string, frames, first, last int64) error {
	flusher, _ := w.(http.Flusher)
	if first < wavHeaderBytes {
		head := wavHeader(frames)[first:min(last+1, wavHeaderBytes)]
		if _, err := w.Write(head); err != nil {
			return err
		}
		if last < wavHeaderBytes {
			return nil
		}
		first = wavHeaderBytes
	}
	offset := first - wavHeaderBytes
	frame, skip := offset/wavFrameBytes, offset%wavFrameBytes
	remaining := last + 1 - first
	at := float64(frame) / narratedRate

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	dec := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-ss", strconv.FormatFloat(at, 'f', 3, 64),
		"-i", src, "-vn", "-f", "s16le", "-ac", fmt.Sprint(narratedChannels), "-ar", fmt.Sprint(narratedRate), "pipe:1")
	out, err := dec.StdoutPipe()
	if err != nil {
		return err
	}
	if err := dec.Start(); err != nil {
		return err
	}
	defer func() { cancel(); _ = dec.Wait() }()
	song := bufio.NewReaderSize(out, 1<<16)

	m := newNarrationMixer(nil, readClip)
	m.frame = frame
	known := -1
	buf := make([]byte, narratedRate/10*wavFrameBytes)
	for remaining > 0 {
		blockStart := float64(m.frame) / narratedRate
		lines, err := g.linesUntil(ctx, videoID, blockStart, blockStart+0.1)
		if err != nil {
			return err
		}
		if len(lines) != known {
			m.refresh(lines)
			known = len(lines)
		}
		n, err := io.ReadFull(song, buf)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			return err
		}
		// The song shorter than it said it was: the header is already sent,
		// so the rest is silence.
		clear(buf[n:])
		m.mix(buf)
		chunk := buf[skip:]
		skip = 0
		if int64(len(chunk)) > remaining {
			chunk = chunk[:remaining]
		}
		if _, err := w.Write(chunk); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		remaining -= int64(len(chunk))
	}
	return nil
}

// linesUntil waits until the pass has been through every line that starts
// in the block [from, until), and answers the lines it has. Only the block:
// a pass working from where the speaker is leaves the start for last, and a
// stream that waited on it sent 0.1s of sound in 40s. It starts a pass when there
// is none, moves it here when it is working somewhere else, and gives up
// waiting after streamLineWait.
func (g *Gateway) linesUntil(ctx context.Context, videoID string, from, until float64) ([]narratedLine, error) {
	started := time.Now()
	asked := false
	for {
		g.narration.mu.Lock()
		pass := g.narration.passes[videoID]
		var status narrationStatus
		var clips []narrationClip
		ready := false
		if pass != nil {
			status = pass.Status
			clips = append(clips, pass.Clips...)
			ready = status == narrationDone || status == narrationFailed ||
				(pass.Total > 0 && spokenIn(pass.cueStarts, pass.spoken, from, until))
		}
		g.narration.mu.Unlock()

		waited := time.Since(started)
		if ready || waited > streamLineWait {
			return g.linesOf(clips), nil
		}
		if pass == nil || status == narrationIdle || (!asked && waited > streamRetargetAfter) {
			g.startNarrationPass(context.WithoutCancel(ctx), videoID, math.Max(until-0.1, 0))
			asked = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// spokenIn reports whether every cue starting in [from, until) is spoken.
func spokenIn(starts []float64, spoken []bool, from, until float64) bool {
	for i, s := range starts {
		if s >= from && s < until && !spoken[i] {
			return false
		}
	}
	return true
}

func (g *Gateway) linesOf(clips []narrationClip) []narratedLine {
	lines := make([]narratedLine, 0, len(clips))
	for _, c := range clips {
		if c.StartsAtUnixMillis != 0 || !strings.HasPrefix(c.ClipURL, "/media/") {
			continue
		}
		lines = append(lines, narratedLine{
			At: c.StartSeconds, Slot: c.DurationSeconds,
			Path: filepath.Join(g.mediaRoot, filepath.FromSlash(strings.TrimPrefix(c.ClipURL, "/media/"))),
		})
	}
	return lines
}

// audioDuration is the song's length, asked of ffprobe once per video.
func (g *Gateway) audioDuration(ctx context.Context, videoID, src string) (float64, error) {
	g.narratedMixes.mu.Lock()
	known, ok := g.narratedMixes.durations[videoID]
	g.narratedMixes.mu.Unlock()
	if ok {
		return known, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1", src).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("ffprobe answered %q", strings.TrimSpace(string(raw)))
	}
	g.narratedMixes.mu.Lock()
	g.narratedMixes.durations[videoID] = seconds
	g.narratedMixes.mu.Unlock()
	return seconds, nil
}
