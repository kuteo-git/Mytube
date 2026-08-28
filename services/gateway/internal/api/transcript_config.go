package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// transcriptConfig is a second machine that can be asked for a video's
// captions.
//
// ## Why
//
// YouTube rate-limits the caption endpoint by **address**, and measured here it
// does so for hours at a time — thirteen straight on 2026-08-27, while videos
// played normally throughout. Nothing in this code can talk its way out of
// that: the block is on the house, not on the request. What can is asking from
// somewhere else, and this household already runs a Home Assistant box that
// could do it.
//
// Deliberately the same three fields and the same rules as ttsConfig and
// translateConfig. A household that has learned one of these forms should not
// have to learn another to do the same thing, and the key rule in particular —
// stored on the server, never sent to the browser, empty means "keep the one
// you have" — is worth having in exactly one shape.
//
// Empty base URL is the ordinary state and means "do not ask anybody else".
// There is no built-in default for the same reason speech has none: a default
// that happens to be right on one machine looks configured to everybody who has
// not configured it.
type transcriptConfig struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

func loadTranscriptConfig(path string) transcriptConfig {
	cfg := transcriptConfig{
		BaseURL: os.Getenv("TRANSCRIPT_BASE_URL"),
		APIKey:  os.Getenv("TRANSCRIPT_API_KEY"),
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	var saved transcriptConfig
	if err := json.Unmarshal(raw, &saved); err != nil {
		// A corrupt file is not a reason to stop the library working.
		return cfg
	}
	if saved.BaseURL != "" {
		cfg.BaseURL = saved.BaseURL
	}
	if saved.APIKey != "" {
		cfg.APIKey = saved.APIKey
	}
	return cfg
}

func saveTranscriptConfig(path string, cfg transcriptConfig) error {
	blob, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// 0600: this holds a credential.
	return withFileLock(path, func() error {
		return writeFileAtomicMode(path, blob, 0o600)
	})
}

// mergeSubmittedTranscriptConfig applies a form submission over what is stored.
//
// An empty key means "keep the one you have" — the browser is never given the
// key, so a save that only changed the URL has nothing to send back, and
// reading that as "clear it" would delete the credential every time.
//
// The base URL is the exception, and it has to be: clearing it is how the
// household turns this off, so an empty one is honoured rather than ignored.
// `clearBaseUrl` says so explicitly instead of leaving an empty string to mean
// two things.
func mergeSubmittedTranscriptConfig(current, submitted transcriptConfig, clearBaseURL bool) transcriptConfig {
	switch {
	case clearBaseURL:
		current.BaseURL = ""
	case submitted.BaseURL != "":
		current.BaseURL = submitted.BaseURL
	}
	if submitted.APIKey != "" {
		current.APIKey = submitted.APIKey
	}
	return current
}

// transcriptURL is the endpoint to GET, from whatever was typed in the field.
//
// A GET with the video id in the query, not a POST with a body: it can be typed
// into a browser, which is the whole difference between "the server is down"
// and "I cannot tell what is wrong" for somebody setting this up on another
// machine at nine in the evening.
//
// It tolerates a base with or without the path, like speechURL, and for the
// same reason — this settings screen already has two fields that disagree about
// what a base URL is, and somebody will paste one into the other.
func transcriptURL(base string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(base), "/")
	if trimmed == "" {
		return ""
	}
	if strings.HasSuffix(trimmed, "/transcript") {
		return trimmed
	}
	return trimmed + "/transcript"
}

func (g *Gateway) transcriptConfigPath() string {
	return filepath.Join(g.configDir, transcriptConfigFile)
}

// The name is shared with ingest, which reads this file rather than being told
// its contents on every request — the caption fetch it belongs to is also
// started by a timer nobody is waiting on.
const transcriptConfigFile = "transcript-config.json"

func (g *Gateway) handleGetTranscriptConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := loadTranscriptConfig(g.transcriptConfigPath())
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl": cfg.BaseURL,
		"hasKey":  cfg.APIKey != "",
		"keyHint": keyHint(cfg.APIKey),
	})
}

type transcriptSubmission struct {
	transcriptConfig
	ClearBaseURL bool `json:"clearBaseUrl"`
}

func (g *Gateway) handleSaveTranscriptConfig(w http.ResponseWriter, r *http.Request) {
	var submitted transcriptSubmission
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&submitted); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	path := g.transcriptConfigPath()
	merged := mergeSubmittedTranscriptConfig(
		loadTranscriptConfig(path), submitted.transcriptConfig, submitted.ClearBaseURL)
	if err := saveTranscriptConfig(path, merged); err != nil {
		g.logger.Warn("transcript config save", "error", err)
		http.Error(w, "could not save", http.StatusServiceUnavailable)
		return
	}
	g.logger.Info("transcript config saved", "baseUrl", merged.BaseURL)
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl": merged.BaseURL,
		"hasKey":  merged.APIKey != "",
		"keyHint": keyHint(merged.APIKey),
	})
}

// transcriptAnswer is what the other machine sends back.
type transcriptAnswer struct {
	Language  string `json:"language"`
	Generated bool   `json:"generated"`
	VTT       string `json:"vtt"`
	Error     string `json:"error"`
}

// handleTestTranscript asks the configured server for one video and reports
// what came back.
//
// It reports the language, the number of cues and the first line rather than a
// verdict, for the reason the speech test already documents: a server can
// answer 200 with an empty transcript, or with the wrong language, and a status
// code calls both of those success. The first line is the part a person can
// look at and know.
func (g *Gateway) handleTestTranscript(w http.ResponseWriter, r *http.Request) {
	var submitted transcriptSubmission
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&submitted)
	cfg := mergeSubmittedTranscriptConfig(
		loadTranscriptConfig(g.transcriptConfigPath()), submitted.transcriptConfig, false)

	videoID := strings.TrimSpace(r.URL.Query().Get("video"))
	if videoID == "" {
		videoID = transcriptTestVideo
	}
	if transcriptURL(cfg.BaseURL) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"error": "no base url"})
		return
	}

	start := time.Now()
	answer, err := fetchRemoteTranscript(r.Context(), cfg, videoID, transcriptLanguages)
	if err != nil {
		// 200 with an error field, not an HTTP error: the request to *this*
		// server succeeded, and what failed is the thing being tested.
		writeJSON(w, http.StatusOK, map[string]any{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"language":  answer.Language,
		"generated": answer.Generated,
		"cues":      countVTTCues(answer.VTT),
		"firstLine": firstVTTLine(answer.VTT),
		"ms":        time.Since(start).Milliseconds(),
	})
}

// A video with captions in both languages this household reads, so the test
// says something about the ordering as well as about the connection.
const transcriptTestVideo = "dQw4w9WgXcQ"

// The languages this app can use, in the order it wants them. Sent to the other
// machine rather than left to it: "Vietnamese if there is any, else English" is
// one rule and it lives on this side, or two servers will disagree about it.
const transcriptLanguages = "vi,en"

func fetchRemoteTranscript(ctx context.Context, cfg transcriptConfig, videoID, langs string) (transcriptAnswer, error) {
	endpoint := transcriptURL(cfg.BaseURL)
	if endpoint == "" {
		return transcriptAnswer{}, fmt.Errorf("no base url")
	}
	q := url.Values{"video_id": {videoID}, "langs": {langs}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return transcriptAnswer{}, err
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}

	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return transcriptAnswer{}, err
	}
	defer func() { _ = res.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return transcriptAnswer{}, err
	}
	if res.StatusCode != http.StatusOK {
		return transcriptAnswer{}, fmt.Errorf("server said %s: %s", res.Status, tailText(string(body), 200))
	}

	var answer transcriptAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return transcriptAnswer{}, fmt.Errorf("not the answer shape: %w", err)
	}
	if answer.Error != "" {
		return transcriptAnswer{}, fmt.Errorf("%s", answer.Error)
	}
	if strings.TrimSpace(answer.VTT) == "" {
		return transcriptAnswer{}, fmt.Errorf("answered with no captions")
	}
	return answer, nil
}

// countVTTCues counts timing lines, which is what a cue is.
func countVTTCues(vtt string) int {
	n := 0
	for _, line := range strings.Split(vtt, "\n") {
		if strings.Contains(line, "-->") {
			n++
		}
	}
	return n
}

// firstVTTLine is the first line of spoken text, for somebody to read and know
// at a glance whether this is the right video and the right language.
func firstVTTLine(vtt string) string {
	lines := strings.Split(vtt, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "-->") {
			continue
		}
		for _, next := range lines[i+1:] {
			if text := strings.TrimSpace(next); text != "" {
				return text
			}
		}
	}
	return ""
}

func tailText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n]
}
