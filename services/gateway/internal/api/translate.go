package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

type translateRequest struct {
	Text string `json:"text"`
	Src  string `json:"src,omitempty"` // e.g. "eng_Latn"
	Tgt  string `json:"tgt,omitempty"` // e.g. "vie_Latn"
}

type translateResponse struct {
	Translated string `json:"translated"`
}

func (g *Gateway) handleTranslate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var req translateRequest
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Text) == "" {
		http.Error(w, "text required", http.StatusBadRequest)
		return
	}

	payload, _ := json.Marshal(map[string]string{
		"text": req.Text,
		"src":  req.Src,
		"tgt":  req.Tgt,
	})

	ctx := r.Context()
	proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://localhost:8005/translate", bytes.NewReader(payload))
	if err != nil {
		g.logger.Warn("translate build request", "error", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := g.streamClient.Do(proxyReq)
	if err != nil {
		g.logger.Warn("translate upstream", "text", req.Text[:min(80, len(req.Text))], "error", err)
		http.Error(w, "translation service unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.logger.Warn("translate status", "status", resp.StatusCode)
		http.Error(w, "translation failed", http.StatusBadGateway)
		return
	}

	var tr translateResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		g.logger.Warn("translate decode", "error", err)
		http.Error(w, "bad translation response", http.StatusBadGateway)
		return
	}

	respBytes, _ := json.Marshal(tr)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)

	g.logger.Info("translate", "text", req.Text[:min(80, len(req.Text))],
		"ms", time.Since(start).Milliseconds(),
		"result", tr.Translated[:min(80, len(tr.Translated))])
}

// handleTranslateBatch forwards a whole batch to the sidecar untouched.
//
// Deliberately a pass-through: deciding whether a batch came back correctly
// aligned belongs where the request was assembled, and that is the sidecar.
// Re-checking here would be a second opinion with less information.
func (g *Gateway) handleTranslateBatch(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		"http://localhost:8005/translate/batch", bytes.NewReader(body))
	if err != nil {
		g.logger.Warn("translate batch build request", "error", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := g.streamClient.Do(proxyReq)
	if err != nil {
		g.logger.Warn("translate batch upstream", "error", err)
		http.Error(w, "translation service unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		g.logger.Warn("translate batch status", "status", resp.StatusCode)
		http.Error(w, "translation failed", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	n, _ := io.Copy(w, resp.Body)
	g.logger.Info("translate batch", "ms", time.Since(start).Milliseconds(), "bytes", n)
}
