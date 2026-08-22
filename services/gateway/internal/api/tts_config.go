package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ttsConfig is where speech is synthesised and how to ask for it.
//
// Deliberately the same four fields, the same file shape and the same rules as
// translateConfig, because they answer the same question about a different
// service — and a household that has learned one form should not have to learn
// another to do the same thing.
//
// ## Why this exists at all
//
// The synthesiser used to be `http://localhost:8002/tts`, written into the
// source, speaking a shape of its own. That is fine for one machine and it is
// the whole obstacle for anybody else: the service lives in a different
// repository, so a person cloning this one has no speech at all and nothing to
// point at instead.
//
// So the app speaks exactly one protocol — OpenAI's — and where it points is a
// setting. VieNeu-TTS learns to answer that protocol rather than this app
// learning to speak VieNeu's; anyone with an OpenAI key, or with any of the
// many services that copy its shape, types a URL and it works.
type ttsConfig struct {
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
	APIKey  string `json:"apiKey"`
	// Voice is typed rather than chosen from a list.
	//
	// OpenAI publishes no endpoint that lists voices — they are a fixed set in
	// its documentation — and every service that imitates the API brings its
	// own names. A menu would be right for exactly one provider and wrong the
	// day that provider added a voice, in the worst way: the voice exists and
	// this app refuses it.
	Voice string `json:"voice"`
}

// loadTTSConfig reads what was saved, falling back to the environment.
//
// No built-in default, and in particular not `http://localhost:8002`. A default
// that happens to be right on the machine this was written on is how a setting
// looks configured to everybody who has not configured it — and then the first
// symptom is silence, which is the hardest fault to trace back to a text field.
func loadTTSConfig(path string) ttsConfig {
	cfg := ttsConfig{
		BaseURL: os.Getenv("TTS_BASE_URL"),
		Model:   os.Getenv("TTS_MODEL"),
		APIKey:  os.Getenv("TTS_API_KEY"),
		Voice:   os.Getenv("TTS_VOICE"),
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	var saved ttsConfig
	if err := json.Unmarshal(raw, &saved); err != nil {
		// A corrupt file is not a reason to stop the library working.
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
	if saved.Voice != "" {
		cfg.Voice = saved.Voice
	}
	return cfg
}

func saveTTSConfig(path string, cfg ttsConfig) error {
	blob, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// 0600: this holds a credential.
	return withFileLock(path, func() error {
		return writeFileAtomicMode(path, blob, 0o600)
	})
}

// mergeSubmittedTTSConfig applies a form submission over what is stored.
//
// An empty key means "keep the one you have", for the reason the translate
// form already documents: the browser is never given the key, so a save that
// only changed the voice has nothing to send back, and reading that as "clear
// it" would delete the credential every time anything else changed.
func mergeSubmittedTTSConfig(current, submitted ttsConfig) ttsConfig {
	if submitted.BaseURL != "" {
		current.BaseURL = submitted.BaseURL
	}
	if submitted.Model != "" {
		current.Model = submitted.Model
	}
	if submitted.APIKey != "" {
		current.APIKey = submitted.APIKey
	}
	if submitted.Voice != "" {
		current.Voice = submitted.Voice
	}
	return current
}

// speechURL is the endpoint to POST to, from whatever was typed in the field.
//
// The field holds the base URL as every provider documents it — including the
// `/v1` — because somebody setting this up will paste what the provider's own
// page told them to use. This app then appends only `/audio/speech`.
//
// It tolerates the other convention too, and that is not politeness: this
// project's *own* translate field works the other way round, appending `/v1`
// itself, so the two forms sit on one settings screen disagreeing about what a
// base URL is. Someone will paste one into the other. Normalising here costs
// three lines and turns a confusing 404 into nothing at all.
//
// A base already ending in `/audio/speech` is left alone, for the person who
// pasted the full endpoint rather than the base.
func speechURL(base string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(base), "/")
	if trimmed == "" {
		return ""
	}
	if strings.HasSuffix(trimmed, "/audio/speech") {
		return trimmed
	}
	if !strings.HasSuffix(trimmed, "/v1") {
		trimmed += "/v1"
	}
	return trimmed + "/audio/speech"
}

func (g *Gateway) ttsConfigPath() string {
	return filepath.Join(g.configDir, "tts-config.json")
}

func (g *Gateway) handleGetTTSConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := loadTTSConfig(g.ttsConfigPath())
	// The key itself never goes to the browser — only enough to tell whether
	// one is set and which one it is.
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl": cfg.BaseURL,
		"model":   cfg.Model,
		"voice":   cfg.Voice,
		"hasKey":  cfg.APIKey != "",
		"keyHint": keyHint(cfg.APIKey),
	})
}

func (g *Gateway) handleSaveTTSConfig(w http.ResponseWriter, r *http.Request) {
	var submitted ttsConfig
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&submitted); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	path := g.ttsConfigPath()
	merged := mergeSubmittedTTSConfig(loadTTSConfig(path), submitted)
	if err := saveTTSConfig(path, merged); err != nil {
		g.logger.Warn("tts config save", "error", err)
		http.Error(w, "could not save", http.StatusServiceUnavailable)
		return
	}
	g.logger.Info("tts config saved",
		"baseUrl", merged.BaseURL, "model", merged.Model, "voice", merged.Voice)
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl": merged.BaseURL,
		"model":   merged.Model,
		"voice":   merged.Voice,
		"hasKey":  merged.APIKey != "",
		"keyHint": keyHint(merged.APIKey),
	})
}

// ttsTestSample is deliberately fixed, so testing one endpoint and then another
// compares like with like.
//
// A Vietnamese sentence with a tone on every syllable and one number: those are
// what a synthesiser reads badly when it reads Vietnamese badly, and hearing it
// once tells you more than any status code.
const ttsTestSample = "Xin chào, đây là bản thử giọng đọc số 1."

// handleTestTTS synthesises one line and reports how it went.
//
// The clip is returned rather than described, because "did it work" is not a
// question a status code can answer for speech: an endpoint can return 200 and
// perfectly formed silence, or a voice reading English phonetics over
// Vietnamese text. The only test that means anything is hearing it.
func (g *Gateway) handleTestTTS(w http.ResponseWriter, r *http.Request) {
	var submitted ttsConfig
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&submitted)
	cfg := mergeSubmittedTTSConfig(loadTTSConfig(g.ttsConfigPath()), submitted)

	if speechURL(cfg.BaseURL) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"error": "no base url"})
		return
	}

	start := time.Now()
	wav, err := g.synthesiseWith(r.Context(), cfg, ttsTestSample, cfg.Voice)
	if err != nil {
		// 200 with an error field, not an HTTP error: the request to *this*
		// server succeeded, and what failed is the thing being tested. A 502
		// here would read as the app being broken.
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sample": ttsTestSample,
		"ms":     time.Since(start).Milliseconds(),
		"bytes":  len(wav),
		// The clip itself, so the answer is something you hear rather than a
		// number you have to believe.
		"audio": "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(wav),
	})
}
