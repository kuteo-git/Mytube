package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ttsRequest is the JSON body the browser sends.
type ttsRequest struct {
	Text  string  `json:"text"`
	Voice string  `json:"voice,omitempty"`
	Speed float64 `json:"speed,omitempty"` // tempo multiplier: 1.0 = natural, 1.4 = fast
	// VideoID says where to keep the result. Optional: without it synthesis
	// still works, it just cannot be cached anywhere.
	VideoID string `json:"videoId,omitempty"`
	// SlotSeconds is how long the line has before the next one is due. When it
	// is given, the caller is asking to be fitted rather than naming a tempo:
	// the server synthesises once at natural speed, measures, and stretches.
	//
	// The client cannot do this arithmetic itself. It knows the slot — that is
	// just the gap between two subtitle timestamps — but the other half is how
	// long the line takes to say, which only exists once the synthesiser has
	// said it. Discovering that in the browser cost a second request at a
	// different tempo, and since tempo is part of the cache key that second
	// request was always a miss. So the lines that needed hurrying were the
	// slowest to arrive, and were then dropped for arriving late.
	SlotSeconds float64 `json:"slotSeconds,omitempty"`
}

// headerSpeed reports the tempo a clip was actually stretched to, so the client
// can keep its own timing honest without measuring the audio again.
const headerSpeed = "X-TTS-Speed"

// handleTTS proxies a single text cue to the VieNeu-TTS micro-service,
// optionally speeds it up with ffmpeg atempo (pitch-preserving), and returns
// the synthesised WAV bytes.
func (g *Gateway) handleTTS(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10)) // 16 KiB
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	var req ttsRequest
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Text) == "" {
		http.Error(w, "invalid request: text is required", http.StatusBadRequest)
		return
	}

	start := time.Now()

	// Fitting the line to its slot, rather than being told a tempo. The natural
	// recording is cached under its own key, so the synthesiser runs once per
	// line for the life of the video however many tempos are asked for later.
	//
	// Checked before the cache below, because at this point the tempo — and so
	// the cache key — is not known yet. Working it out is the first thing
	// serveFitted does.
	if req.SlotSeconds > 0 {
		g.serveFitted(w, r, req, start)
		return
	}

	// Synthesis is the expensive half of narration and its output never changes
	// for the same words at the same tempo, so it is kept on disk beside the
	// video. Without this every viewing re-ran the TTS engine and ffmpeg for
	// every line, including lines the same viewer had already heard.
	if wav, ok := readTTSCache(g.mediaRoot, req.VideoID, req.Text, req.Speed, req.Voice); ok {
		w.Header().Set(headerSpeed, fmt.Sprintf("%.2f", req.Speed))
		writeWAV(w, wav)
		return
	}

	synthBytes, err := g.synthesise(r.Context(), req.Text, req.Voice)
	if err != nil {
		g.replySynthError(w, req.Text, err)
		return
	}

	// Apply pitch-preserving tempo via ffmpeg when the caller asks for a
	// speed different from 1.0.  Web Audio API playbackRate shifts pitch;
	// ffmpeg's atempo (WSOLA) does not.
	if req.Speed > 0 && math.Abs(req.Speed-1.0) > 0.005 {
		synthBytes, err = atempo(synthBytes, req.Speed)
		if err != nil {
			g.logger.Warn("tts atempo", "error", err)
			http.Error(w, "tempo adjustment failed", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set(headerSpeed, fmt.Sprintf("%.2f", req.Speed))

	// Keep it beside the video before answering. The key already includes the
	// speed, so a stretched clip is as cacheable as an unstretched one — the
	// old `no-cache` for speed-adjusted audio meant narration, which always runs
	// at 1.1, was the one thing that could never be cached.
	if err := writeTTSCache(g.mediaRoot, req.VideoID, req.Text, req.Speed, req.Voice, synthBytes); err != nil {
		g.logger.Warn("tts cache write", "error", err)
	}

	written := writeWAV(w, synthBytes)
	g.logger.Info("tts", "text", req.Text[:min(80, len(req.Text))],
		"speed", fmt.Sprintf("%.2f", req.Speed),
		"ms", time.Since(start).Milliseconds(),
		"bytes", written)
}

// errUpstream marks a failure that came from the synthesiser rather than from
// anything this service did, so the caller can answer 502 rather than 500.
var errUpstream = errors.New("tts upstream")

// synthesise asks VieNeu-TTS for one line, at its natural tempo.
//
// The context comes from the inbound request, so a browser that abandons its
// fetch — the viewer left the video, or changed voice mid-sweep — cancels the
// work here rather than leaving the machine synthesising for a page nobody is
// on. With pre-generation walking a whole video that matters far more than it
// did when clips were made a few seconds ahead of the playhead.
func (g *Gateway) synthesise(ctx context.Context, text, voice string) ([]byte, error) {
	payload, _ := json.Marshal(map[string]string{
		"input":   text,
		"voice":   voice,
		"emotion": "storytelling",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://localhost:8002/tts", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errUpstream, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", errUpstream, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (g *Gateway) replySynthError(w http.ResponseWriter, text string, err error) {
	g.logger.Warn("tts upstream", "text", text[:min(80, len(text))], "error", err)
	if errors.Is(err, errUpstream) {
		http.Error(w, "tts service unreachable", http.StatusBadGateway)
		return
	}
	http.Error(w, "internal", http.StatusInternalServerError)
}

func writeWAV(w http.ResponseWriter, wav []byte) int {
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.WriteHeader(http.StatusOK)
	n, _ := w.Write(wav)
	return n
}

// statusTooFast tells the client this line cannot be said in the time it has.
//
// A distinct status rather than an empty 200, because the client's answer is to
// skip the cue and carry on — and "no audio" has to be told apart from "the
// synthesiser is down", which is answered by backing off and trying again.
const statusTooFast = http.StatusUnprocessableEntity

// serveFitted synthesises a line once and stretches it to fit its slot.
//
//  1. the natural recording, from cache or from the synthesiser
//  2. measure it
//  3. work out the tempo that fits — or refuse the line outright
//  4. the stretched copy, from cache or from ffmpeg
//
// Only step 1 can ever run the TTS model, and its cache key has no tempo in it.
// So a line is synthesised once for the life of the video no matter how often
// the timing around it changes.
func (g *Gateway) serveFitted(w http.ResponseWriter, r *http.Request, req ttsRequest, start time.Time) {
	natural, cached := readTTSCache(g.mediaRoot, req.VideoID, req.Text, naturalSpeed, req.Voice)
	if !cached {
		var err error
		natural, err = g.synthesise(r.Context(), req.Text, req.Voice)
		if err != nil {
			g.replySynthError(w, req.Text, err)
			return
		}
		if err := writeTTSCache(g.mediaRoot, req.VideoID, req.Text,
			naturalSpeed, req.Voice, natural); err != nil {
			g.logger.Warn("tts cache write", "error", err)
		}
	}

	// An unmeasurable clip is served as it is rather than refused. Whatever the
	// synthesiser returned, it is closer to narration than silence, and the only
	// thing lost is the fitting.
	duration, ok := wavDuration(natural)
	if !ok {
		g.logger.Warn("tts unmeasurable wav", "bytes", len(natural))
		w.Header().Set(headerSpeed, fmt.Sprintf("%.2f", naturalSpeed))
		writeWAV(w, natural)
		return
	}

	speed, err := tempoFor(duration, req.SlotSeconds)
	if err != nil {
		// Refused, not clamped. Squeezing it to maxSpeed and playing it anyway
		// cost two lines to keep one: unintelligible at that tempo, and it
		// overran its slot, which pushed the next clip past its own cue.
		g.logger.Info("tts too fast", "text", req.Text[:min(80, len(req.Text))],
			"natural", fmt.Sprintf("%.2f", duration),
			"slot", fmt.Sprintf("%.2f", req.SlotSeconds),
			"needed", fmt.Sprintf("%.2f", duration/req.SlotSeconds))
		w.Header().Set(headerSpeed, fmt.Sprintf("%.2f", duration/req.SlotSeconds))
		writeJSON(w, statusTooFast, map[string]any{
			"tooFast":     true,
			"neededSpeed": duration / req.SlotSeconds,
			"maxSpeed":    maxSpeed,
		})
		return
	}

	fitted := natural
	if math.Abs(speed-naturalSpeed) > 0.005 {
		if stretched, ok := readTTSCache(g.mediaRoot, req.VideoID, req.Text,
			speed, req.Voice); ok {
			fitted = stretched
		} else {
			fitted, err = atempo(natural, speed)
			if err != nil {
				g.logger.Warn("tts atempo", "error", err)
				http.Error(w, "tempo adjustment failed", http.StatusInternalServerError)
				return
			}
			if err := writeTTSCache(g.mediaRoot, req.VideoID, req.Text,
				speed, req.Voice, fitted); err != nil {
				g.logger.Warn("tts cache write", "error", err)
			}
		}
	}

	w.Header().Set(headerSpeed, fmt.Sprintf("%.2f", speed))
	written := writeWAV(w, fitted)
	g.logger.Info("tts fitted", "text", req.Text[:min(80, len(req.Text))],
		"natural", fmt.Sprintf("%.2f", duration),
		"slot", fmt.Sprintf("%.2f", req.SlotSeconds),
		"speed", fmt.Sprintf("%.2f", speed),
		"synthesised", !cached,
		"ms", time.Since(start).Milliseconds(),
		"bytes", written)
}

// atempo runs ffmpeg to time-stretch WAV bytes by factor (pitch-preserving).
// ffmpeg's single atempo is limited to [0.5, 2.0]; for larger factors we chain
// multiple filters, e.g. 2.5× → atempo=2.0,atempo=1.25.
func atempo(wav []byte, factor float64) ([]byte, error) {
	filter := buildAtempoChain(factor)

	cmd := exec.Command("ffmpeg",
		"-loglevel", "error",
		"-i", "pipe:0",
		"-filter:a", filter,
		"-f", "wav",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(wav)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil // logged at caller

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg atempo %.2f: %w", factor, err)
	}
	return out.Bytes(), nil
}

// buildAtempoChain returns a comma-separated atempo filter chain for `factor`.
// Each atempo instance is clamped to [0.5, 2.0]; for a factor of 2.5 this
// produces "atempo=2.0,atempo=1.25".
func buildAtempoChain(factor float64) string {
	const minF, maxF = 0.5, 2.0
	if factor >= minF && factor <= maxF {
		return fmt.Sprintf("atempo=%.4f", factor)
	}
	var parts []string
	remaining := factor
	for remaining > maxF {
		parts = append(parts, fmt.Sprintf("atempo=%.4f", maxF))
		remaining /= maxF
	}
	if remaining < minF {
		// Scale the last filter down to hit the exact target.
		parts[len(parts)-1] = fmt.Sprintf("atempo=%.4f", remaining*maxF)
	} else {
		parts = append(parts, fmt.Sprintf("atempo=%.4f", remaining))
	}
	return strings.Join(parts, ",")
}

// handleTTSVoices lists the voices the synthesiser offers.
//
// Only the list. The service also reports its own current voice, but that is a
// global default this app never sets: every synthesis request names its voice,
// because the voice is a per-device preference and two people in the house
// should be able to disagree about it.
func (g *Gateway) handleTTSVoices(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		"http://localhost:8002/voice", nil)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	resp, err := g.streamClient.Do(req)
	if err != nil {
		g.logger.Warn("tts voices", "error", err)
		http.Error(w, "tts service unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	var body struct {
		Voices []string `json:"voices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "unreadable voice list", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"voices": body.Voices})
}
