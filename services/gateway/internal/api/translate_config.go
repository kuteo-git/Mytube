package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// translateConfig is where the translator points and what it asks for.
//
// Held by the gateway rather than the sidecar, and sent down with every batch,
// so the sidecar stays stateless: there is no file for the two to disagree
// about and no question of whether a change needs a restart. The batch after
// the save uses the new settings.
type translateConfig struct {
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
	APIKey  string `json:"apiKey"`
}

// loadTranslateConfig reads what was saved, falling back to the environment.
//
// The environment stays the default rather than being migrated into the file on
// first run: until somebody presses Save, the deployment behaves exactly as
// .env.local says, and deleting the file returns it there.
func loadTranslateConfig(path string) translateConfig {
	cfg := translateConfig{
		BaseURL: os.Getenv("OMNIROUTE_BASE_URL"),
		Model:   os.Getenv("OMNIROUTE_MODEL"),
		APIKey:  os.Getenv("OMNIROUTE_API_KEY"),
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	var saved translateConfig
	if err := json.Unmarshal(raw, &saved); err != nil {
		// A corrupt file is not a reason to stop translating.
		return cfg
	}
	if saved.BaseURL != "" {
		cfg.BaseURL = saved.BaseURL
	}
	if saved.Model != "" {
		cfg.Model = saved.Model
	}
	if saved.APIKey != "" {
		cfg.APIKey = saved.APIKey
	}
	return cfg
}

func saveTranslateConfig(path string, cfg translateConfig) error {
	blob, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// 0600: this holds a credential.
	return withFileLock(path, func() error {
		return writeFileAtomicMode(path, blob, 0o600)
	})
}

// mergeSubmittedConfig applies a form submission over what is already stored.
//
// An empty key means "keep the one you have". It has to: the browser is never
// given the key, so a form saving a model change has nothing to send back, and
// treating that as "clear it" would delete the credential every time somebody
// changed anything else.
func mergeSubmittedConfig(current, submitted translateConfig) translateConfig {
	if submitted.BaseURL != "" {
		current.BaseURL = submitted.BaseURL
	}
	if submitted.Model != "" {
		current.Model = submitted.Model
	}
	if submitted.APIKey != "" {
		current.APIKey = submitted.APIKey
	}
	return current
}

// keyHint is enough to recognise a key by and not enough to use.
func keyHint(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "…"
	}
	return "…" + key[len(key)-4:]
}

// providerURL builds an endpoint from whatever was typed in a base URL field.
//
// This field has always taken a base *without* `/v1` and appended the rest
// itself, while every provider's documentation — and this project's own speech
// field, added later — gives the base *with* it. Two inputs on one settings
// screen disagreeing about what a base URL is, and somebody will paste one into
// the other: `/v1/v1/models`, and a 404 that explains nothing.
//
// Both are accepted. The same rule as speechURL, written twice rather than
// shared because the two live either side of a service boundary — and stated
// here so the next reader knows there is a second copy in translate_server.py.
func providerURL(base, path string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(base), "/")
	if trimmed == "" {
		return ""
	}
	if !strings.HasSuffix(trimmed, "/v1") {
		trimmed += "/v1"
	}
	return trimmed + "/" + path
}

func (g *Gateway) translateConfigPath() string {
	return filepath.Join(g.configDir, "translate-config.json")
}

func (g *Gateway) handleGetTranslateConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := loadTranslateConfig(g.translateConfigPath())
	// The key itself never goes to the browser — only enough to tell whether
	// one is set and which one it is.
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl": cfg.BaseURL,
		"model":   cfg.Model,
		"hasKey":  cfg.APIKey != "",
		"keyHint": keyHint(cfg.APIKey),
	})
}

func (g *Gateway) handleSaveTranslateConfig(w http.ResponseWriter, r *http.Request) {
	var submitted translateConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&submitted); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	path := g.translateConfigPath()
	merged := mergeSubmittedConfig(loadTranslateConfig(path), submitted)
	if err := saveTranslateConfig(path, merged); err != nil {
		g.logger.Warn("translate config save", "error", err)
		http.Error(w, "could not save", http.StatusServiceUnavailable)
		return
	}
	g.logger.Info("translate config saved", "model", merged.Model, "baseUrl", merged.BaseURL)
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl": merged.BaseURL,
		"model":   merged.Model,
		"hasKey":  merged.APIKey != "",
		"keyHint": keyHint(merged.APIKey),
	})
}

// handleTranslateModels asks the provider what it can do.
//
// Proxied rather than called from the browser: the page would otherwise need
// the key in its own memory and would meet whatever CORS policy the provider
// happens to have.
func (g *Gateway) handleTranslateModels(w http.ResponseWriter, r *http.Request) {
	stored := loadTranslateConfig(g.translateConfigPath())
	base := r.URL.Query().Get("baseUrl")
	if base == "" {
		base = stored.BaseURL
	}
	key := r.URL.Query().Get("apiKey")
	if key == "" {
		key = stored.APIKey
	}
	if base == "" {
		http.Error(w, "no base url", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet,
		providerURL(base, "models"), nil)
	if err != nil {
		http.Error(w, "bad base url", http.StatusBadRequest)
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := g.streamClient.Do(req)
	if err != nil {
		g.logger.Warn("translate models", "error", err)
		http.Error(w, "provider unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("provider returned %d", resp.StatusCode), http.StatusBadGateway)
		return
	}

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		http.Error(w, "unreadable model list", http.StatusBadGateway)
		return
	}
	ids := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		ids = append(ids, m.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": ids})
}

// translateTestSample is deliberately fixed, so pressing Test on one model and
// then another compares like with like.
//
// These two clauses are the ones this project has already been caught by:
// "you" came back as "anh" — addressing one man, formally — and "the next one"
// as "phim tiếp theo", a film rather than a video.
const translateTestSample = "So if you're watching this, I'll see you in the next one."

func (g *Gateway) handleTranslateTest(w http.ResponseWriter, r *http.Request) {
	var submitted translateConfig
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&submitted)
	cfg := mergeSubmittedConfig(loadTranslateConfig(g.translateConfigPath()), submitted)

	payload, _ := json.Marshal(map[string]any{
		"cues":    []string{translateTestSample},
		"context": []string{},
		"baseUrl": cfg.BaseURL,
		"model":   cfg.Model,
		"apiKey":  cfg.APIKey,
	})

	start := time.Now()
	out, err := g.postSidecarBatch(r, payload)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}
	translated := ""
	if len(out.Translations) > 0 {
		translated = out.Translations[0]
	}
	if translated == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"error": "the model returned nothing for the sample",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sample":     translateTestSample,
		"translated": translated,
		"ms":         time.Since(start).Milliseconds(),
	})
}
