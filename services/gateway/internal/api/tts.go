package api

import (
	"bytes"
	"encoding/json"
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
}

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

	// Forward to VieNeu-TTS (running on the same machine, port 8004 is the
	// dedicated narration instance with the "update" checkpoint).
	ttsURL := "http://localhost:8002/tts"
	payload, _ := json.Marshal(map[string]string{
		"input":   req.Text,
		"voice":   req.Voice,
		"emotion": "storytelling",
	})

	ctx := r.Context()
	proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, ttsURL, bytes.NewReader(payload))
	if err != nil {
		g.logger.Warn("tts build request", "error", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := g.streamClient.Do(proxyReq)
	if err != nil {
		g.logger.Warn("tts upstream", "text", req.Text[:min(80, len(req.Text))], "error", err)
		http.Error(w, "tts service unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.logger.Warn("tts upstream status", "status", resp.StatusCode)
		http.Error(w, "tts synthesis failed", http.StatusBadGateway)
		return
	}

	synthBytes, _ := io.ReadAll(resp.Body)

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

	w.Header().Set("Content-Type", "audio/wav")
	// Don't cache speed-adjusted audio — different speeds for the same
	// text produce different bytes.
	if math.Abs(req.Speed-1.0) > 0.005 {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=604800")
	}
	w.WriteHeader(http.StatusOK)

	written, _ := w.Write(synthBytes)
	g.logger.Info("tts", "text", req.Text[:min(80, len(req.Text))],
		"speed", fmt.Sprintf("%.2f", req.Speed),
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
