package api

import (
	"bytes"
	"io"
	"net/http"
	"time"
)

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
