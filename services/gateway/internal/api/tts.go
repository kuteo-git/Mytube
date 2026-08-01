package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// ttsRequest is the JSON body the browser sends.
type ttsRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice,omitempty"`
	Speed string `json:"speed,omitempty"` // faster/normal/slower (unused by VieNeu v3 but kept for future)
}

// handleTTS proxies a single text cue to the VieNeu-TTS micro-service and
// returns the synthesised WAV bytes. The service is a local GPU process that
// runs independently of the stack; this is just a thin HTTP pass-through so
// the browser never needs to know the TTS host:port directly.
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
	ttsURL := "http://localhost:8004/tts"
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

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "public, max-age=604800") // 7 days — same text + same voice = same audio
	w.WriteHeader(http.StatusOK)

	written, _ := io.Copy(w, resp.Body)
	g.logger.Info("tts", "text", req.Text[:min(80, len(req.Text))], "ms", time.Since(start).Milliseconds(), "bytes", written)
}
