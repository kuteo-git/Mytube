package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// sidecarBatchResponse is what the translation sidecar answers with.
type sidecarBatchResponse struct {
	Translations []string `json:"translations"`
	// Snake case because that is what the sidecar sends, and the browser has
	// been reading this shape since before the gateway decoded it.
	FellBack bool `json:"fell_back"`
}

// postSidecarBatch sends an already-assembled body to the sidecar and reads the
// answer. Shared by the batch route and the settings page's Test button, so the
// two cannot drift apart about how a batch is sent.
func (g *Gateway) postSidecarBatch(r *http.Request, body []byte) (sidecarBatchResponse, error) {
	return g.postSidecarBatchCtx(r.Context(), body)
}

// postSidecarBatchCtx is the same call without a request to take a context from.
//
// The narration pass has none: it is started by a request that has already
// answered 202 and outlives it deliberately, so tying it to that request's
// context would cancel the pass the instant the handler returned.
func (g *Gateway) postSidecarBatchCtx(ctx context.Context, body []byte) (sidecarBatchResponse, error) {
	var out sidecarBatchResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://localhost:8005/translate/batch", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.streamClient.Do(req)
	if err != nil {
		return out, fmt.Errorf("translation service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("translation service returned %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return out, fmt.Errorf("unreadable answer: %w", err)
	}
	return out, nil
}

// handleTranslateBatch forwards a batch to the sidecar, with the configured
// provider merged in.
//
// The browser sends only the cues; where to send them and under whose key is
// the gateway's business, and keeping it that way is what stops the API key
// ever being in a page.
func (g *Gateway) handleTranslateBatch(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	cfg := loadTranslateConfig(g.translateConfigPath())
	body["baseUrl"] = cfg.BaseURL
	body["model"] = cfg.Model
	body["apiKey"] = cfg.APIKey
	merged, _ := json.Marshal(body)

	start := time.Now()
	out, err := g.postSidecarBatch(r, merged)
	if err != nil {
		g.logger.Warn("translate batch", "error", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	g.logger.Info("translate batch", "ms", time.Since(start).Milliseconds(),
		"lines", len(out.Translations), "fellBack", out.FellBack)
	writeJSON(w, http.StatusOK, out)
}
